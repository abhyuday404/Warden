package detect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/abhyuday404/Warden/internal/domain"
)

const maxFiles = 5000

var ignoredDirs = map[string]bool{
	".git": true, ".warden": true, ".prava-deploy": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, ".next": true, ".venv": true, "venv": true,
}

type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func Inspect(root string) (domain.ProjectSpec, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return domain.ProjectSpec{}, fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return domain.ProjectSpec{}, fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return domain.ProjectSpec{}, fmt.Errorf("project root must be a directory")
	}

	files, err := collect(abs)
	if err != nil {
		return domain.ProjectSpec{}, err
	}
	spec := domain.ProjectSpec{
		Schema: domain.SchemaVersion, Root: abs, Name: filepath.Base(abs), Kind: domain.ProjectUnknown,
		Runtime: "unknown", Build: domain.BuildBuildpack, Port: 8080, Metadata: map[string]string{},
	}

	if has(files, "Dockerfile") || has(files, "dockerfile") {
		spec.Kind, spec.Runtime, spec.Build = domain.ProjectContainer, "container", domain.BuildDockerfile
		spec.Signals = append(spec.Signals, domain.Signal{Name: "dockerfile", Path: findCaseInsensitive(files, "Dockerfile"), Confidence: 100})
		if port := dockerPort(filepath.Join(abs, findCaseInsensitive(files, "Dockerfile"))); port > 0 {
			spec.Port = port
		}
	}

	if path := findCaseInsensitive(files, "package.json"); path != "" {
		applyNode(&spec, filepath.Join(abs, path), path)
	}
	if has(files, "go.mod") {
		applyGo(&spec, abs, files)
	}
	if has(files, "pyproject.toml") || has(files, "requirements.txt") || has(files, "Pipfile") {
		applyPython(&spec, files)
	}
	if has(files, "Cargo.toml") {
		applyRuntime(&spec, "rust", domain.ProjectService, "cargo build --release", "cargo run --release", "Cargo.toml")
	}
	if has(files, "pom.xml") || has(files, "build.gradle") || has(files, "build.gradle.kts") {
		applyRuntime(&spec, "java", domain.ProjectService, "", "", firstExisting(files, "pom.xml", "build.gradle", "build.gradle.kts"))
	}
	if spec.Kind == domain.ProjectUnknown && has(files, "index.html") {
		spec.Kind, spec.Runtime, spec.Build, spec.OutputDir = domain.ProjectStatic, "static", domain.BuildStatic, "."
		spec.Signals = append(spec.Signals, domain.Signal{Name: "html-entrypoint", Path: "index.html", Confidence: 90})
	}

	if spec.Build == domain.BuildDockerfile {
		spec.Kind = domain.ProjectContainer
	}
	spec.Environment = envKeys(filepath.Join(abs, ".env.example"))
	spec.Capabilities = inferCapabilities(spec)
	sort.Slice(spec.Signals, func(i, j int) bool { return spec.Signals[i].Confidence > spec.Signals[j].Confidence })
	return spec, nil
}

func collect(root string) (map[string]string, error) {
	files := map[string]string{}
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count > maxFiles {
			return filepath.SkipAll
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk project: %w", err)
	}
	return files, nil
}

func applyNode(spec *domain.ProjectSpec, path, rel string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pkg packageJSON
	if json.Unmarshal(b, &pkg) != nil {
		return
	}
	if pkg.Name != "" {
		spec.Name = pkg.Name
	}
	if spec.Runtime == "unknown" {
		spec.Runtime, spec.Kind, spec.Build = "node", domain.ProjectService, domain.BuildBuildpack
	}
	spec.Signals = append(spec.Signals, domain.Signal{Name: "node-package", Path: rel, Confidence: 90})
	deps := map[string]bool{}
	for k := range pkg.Dependencies {
		deps[k] = true
	}
	for k := range pkg.DevDependencies {
		deps[k] = true
	}
	switch {
	case deps["next"]:
		spec.Framework, spec.Kind = "nextjs", domain.ProjectService
		spec.BuildCommand, spec.StartCommand, spec.OutputDir = script(pkg, "build", "npm run build"), script(pkg, "start", "npm start"), ".next"
	case deps["@remix-run/node"]:
		spec.Framework, spec.Kind = "remix", domain.ProjectService
	case deps["astro"]:
		spec.Framework, spec.Kind, spec.OutputDir = "astro", domain.ProjectStatic, "dist"
		spec.BuildCommand = script(pkg, "build", "npm run build")
	case deps["vite"]:
		spec.Framework, spec.Kind, spec.OutputDir = "vite", domain.ProjectStatic, "dist"
		spec.BuildCommand = script(pkg, "build", "npm run build")
	case deps["react"]:
		spec.Framework, spec.Kind, spec.OutputDir = "react", domain.ProjectStatic, "build"
		spec.BuildCommand = script(pkg, "build", "npm run build")
	case deps["express"] || deps["fastify"] || deps["koa"]:
		spec.Framework, spec.Kind = "node-http", domain.ProjectService
	}
	if spec.StartCommand == "" && pkg.Scripts["start"] != "" {
		spec.StartCommand = "npm start"
	}
	if spec.BuildCommand == "" && pkg.Scripts["build"] != "" {
		spec.BuildCommand = "npm run build"
	}
}

func applyGo(spec *domain.ProjectSpec, root string, files map[string]string) {
	if spec.Runtime == "unknown" {
		spec.Runtime, spec.Kind, spec.Build = "go", domain.ProjectService, domain.BuildBuildpack
	}
	spec.Signals = append(spec.Signals, domain.Signal{Name: "go-module", Path: "go.mod", Confidence: 95})
	mainPath := ""
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		path := files[rel]
		if !strings.HasSuffix(strings.ToLower(rel), ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err == nil && regexp.MustCompile(`(?m)^package\s+main\s*$`).Match(b) {
			mainPath = filepath.ToSlash(filepath.Dir(rel))
			break
		}
	}
	if mainPath == "." || mainPath == "" {
		spec.BuildCommand, spec.StartCommand = "go build -o app .", "./app"
	} else {
		spec.BuildCommand, spec.StartCommand = "go build -o app ./"+mainPath, "./app"
		spec.Metadata["go_main"] = mainPath
	}
	_ = root
}

func applyPython(spec *domain.ProjectSpec, files map[string]string) {
	if spec.Runtime == "unknown" {
		spec.Runtime, spec.Kind, spec.Build = "python", domain.ProjectService, domain.BuildBuildpack
	}
	signal := firstExisting(files, "pyproject.toml", "requirements.txt", "Pipfile")
	spec.Signals = append(spec.Signals, domain.Signal{Name: "python-project", Path: signal, Confidence: 90})
	for _, candidate := range []string{"app.py", "main.py", "manage.py"} {
		if has(files, candidate) {
			spec.StartCommand = "python " + candidate
			break
		}
	}
}

func applyRuntime(spec *domain.ProjectSpec, runtime string, kind domain.ProjectKind, build, start, signal string) {
	if spec.Runtime != "unknown" {
		return
	}
	spec.Runtime, spec.Kind, spec.Build = runtime, kind, domain.BuildBuildpack
	spec.BuildCommand, spec.StartCommand = build, start
	spec.Signals = append(spec.Signals, domain.Signal{Name: runtime + "-project", Path: signal, Confidence: 90})
}

func inferCapabilities(spec domain.ProjectSpec) []domain.Capability {
	set := map[domain.Capability]bool{domain.CapabilityCustomDomain: true}
	if spec.Kind == domain.ProjectStatic {
		set[domain.CapabilityStatic] = true
	}
	if spec.Kind == domain.ProjectContainer || spec.Build == domain.BuildDockerfile {
		set[domain.CapabilityContainer] = true
		set[domain.CapabilityTCP] = true
	}
	if spec.Build == domain.BuildBuildpack {
		set[domain.CapabilityBuildpack] = true
	}
	if spec.Kind == domain.ProjectService {
		set[domain.CapabilityTCP] = true
	}
	if spec.Kind == domain.ProjectWorker {
		set[domain.CapabilityWorker] = true
	}
	result := make([]domain.Capability, 0, len(set))
	for c := range set {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func dockerPort(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	m := regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`).FindSubmatch(b)
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

func envKeys(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var keys []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if ok && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func script(pkg packageJSON, name, fallback string) string {
	if pkg.Scripts[name] != "" {
		return fallback
	}
	return ""
}

func has(files map[string]string, rel string) bool { _, ok := files[filepath.ToSlash(rel)]; return ok }

func firstExisting(files map[string]string, names ...string) string {
	for _, n := range names {
		if has(files, n) {
			return n
		}
	}
	return ""
}

func findCaseInsensitive(files map[string]string, name string) string {
	for rel := range files {
		if strings.EqualFold(rel, name) {
			return rel
		}
	}
	return ""
}
