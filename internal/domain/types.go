package domain

import "time"

const SchemaVersion = "v1"

type ProjectKind string

const (
	ProjectStatic    ProjectKind = "static"
	ProjectService   ProjectKind = "service"
	ProjectWorker    ProjectKind = "worker"
	ProjectContainer ProjectKind = "container"
	ProjectUnknown   ProjectKind = "unknown"
)

type BuildStrategy string

const (
	BuildStatic     BuildStrategy = "static"
	BuildDockerfile BuildStrategy = "dockerfile"
	BuildBuildpack  BuildStrategy = "buildpack"
	BuildNative     BuildStrategy = "native"
)

type Capability string

const (
	CapabilityStatic       Capability = "static"
	CapabilityContainer    Capability = "container"
	CapabilityBuildpack    Capability = "buildpack"
	CapabilityFunctions    Capability = "functions"
	CapabilityWorker       Capability = "worker"
	CapabilityVolume       Capability = "volume"
	CapabilityCustomDomain Capability = "custom_domain"
	CapabilityRegions      Capability = "regions"
	CapabilityTCP          Capability = "tcp"
)

type Signal struct {
	Name       string `json:"name" yaml:"name"`
	Path       string `json:"path" yaml:"path"`
	Confidence int    `json:"confidence" yaml:"confidence"`
}

type ProjectSpec struct {
	Schema       string            `json:"schema" yaml:"schema"`
	Root         string            `json:"root" yaml:"root"`
	Name         string            `json:"name" yaml:"name"`
	Kind         ProjectKind       `json:"kind" yaml:"kind"`
	Runtime      string            `json:"runtime" yaml:"runtime"`
	Framework    string            `json:"framework,omitempty" yaml:"framework,omitempty"`
	Build        BuildStrategy     `json:"build" yaml:"build"`
	BuildCommand string            `json:"build_command,omitempty" yaml:"build_command,omitempty"`
	StartCommand string            `json:"start_command,omitempty" yaml:"start_command,omitempty"`
	OutputDir    string            `json:"output_dir,omitempty" yaml:"output_dir,omitempty"`
	Port         int               `json:"port,omitempty" yaml:"port,omitempty"`
	Capabilities []Capability      `json:"capabilities" yaml:"capabilities"`
	Signals      []Signal          `json:"signals" yaml:"signals"`
	Environment  []string          `json:"environment,omitempty" yaml:"environment,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type Money struct {
	Amount   string `json:"amount" yaml:"amount"`
	Currency string `json:"currency" yaml:"currency"`
}

type CostEstimate struct {
	Kind       string   `json:"kind" yaml:"kind"`
	MonthlyMin Money    `json:"monthly_min" yaml:"monthly_min"`
	MonthlyMax Money    `json:"monthly_max" yaml:"monthly_max"`
	Confidence string   `json:"confidence" yaml:"confidence"`
	Notes      []string `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type BillingMode string

const (
	BillingNone            BillingMode = "none"
	BillingExistingAccount BillingMode = "existing_account"
	BillingPrepaidCredits  BillingMode = "prepaid_credits"
	BillingFixedCheckout   BillingMode = "fixed_checkout"
	BillingMetered         BillingMode = "metered"
)

type ProviderInfo struct {
	ID                string       `json:"id" yaml:"id"`
	Name              string       `json:"name" yaml:"name"`
	Description       string       `json:"description" yaml:"description"`
	Capabilities      []Capability `json:"capabilities" yaml:"capabilities"`
	Billing           BillingMode  `json:"billing" yaml:"billing"`
	MerchantURL       string       `json:"merchant_url,omitempty" yaml:"merchant_url,omitempty"`
	Country           string       `json:"country,omitempty" yaml:"country,omitempty"`
	Available         bool         `json:"available" yaml:"available"`
	UnavailableReason string       `json:"unavailable_reason,omitempty" yaml:"unavailable_reason,omitempty"`
}

type Plan struct {
	ID                string            `json:"id" yaml:"id"`
	CreatedAt         time.Time         `json:"created_at" yaml:"created_at"`
	Project           ProjectSpec       `json:"project" yaml:"project"`
	Provider          ProviderInfo      `json:"provider" yaml:"provider"`
	Required          []Capability      `json:"required" yaml:"required"`
	Cost              CostEstimate      `json:"cost" yaml:"cost"`
	Budget            Money             `json:"budget" yaml:"budget"`
	BudgetRequired    bool              `json:"budget_required" yaml:"budget_required"`
	AuthorizationMode string            `json:"authorization_mode" yaml:"authorization_mode"`
	Warnings          []string          `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	ProviderConfig    map[string]string `json:"provider_config,omitempty" yaml:"provider_config,omitempty"`
}

type PaymentStatus string

const (
	PaymentNotRequired PaymentStatus = "not_required"
	PaymentPending     PaymentStatus = "awaiting_approval"
	PaymentAuthorized  PaymentStatus = "authorized"
	PaymentExpired     PaymentStatus = "expired"
	PaymentDeclined    PaymentStatus = "declined"
	PaymentFailed      PaymentStatus = "failed"
)

type BudgetAuthorization struct {
	ID             string        `json:"id" yaml:"id"`
	PlanID         string        `json:"plan_id" yaml:"plan_id"`
	ProviderID     string        `json:"provider_id" yaml:"provider_id"`
	MerchantURL    string        `json:"merchant_url" yaml:"merchant_url"`
	Budget         Money         `json:"budget" yaml:"budget"`
	Cadence        string        `json:"cadence" yaml:"cadence"`
	Status         PaymentStatus `json:"status" yaml:"status"`
	ApprovalURL    string        `json:"approval_url,omitempty" yaml:"approval_url,omitempty"`
	ExternalID     string        `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	IdempotencyKey string        `json:"idempotency_key" yaml:"idempotency_key"`
	CreatedAt      time.Time     `json:"created_at" yaml:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at" yaml:"updated_at"`
}

type DeploymentStatus string

const (
	DeploymentPlanned      DeploymentStatus = "planned"
	DeploymentProvisioning DeploymentStatus = "provisioning"
	DeploymentLive         DeploymentStatus = "live"
	DeploymentDegraded     DeploymentStatus = "degraded"
	DeploymentFailed       DeploymentStatus = "failed"
	DeploymentDestroying   DeploymentStatus = "destroying"
	DeploymentDestroyed    DeploymentStatus = "destroyed"
)

type Deployment struct {
	ID              string            `json:"id" yaml:"id"`
	PlanID          string            `json:"plan_id" yaml:"plan_id"`
	ProviderID      string            `json:"provider_id" yaml:"provider_id"`
	ProviderRef     string            `json:"provider_ref,omitempty" yaml:"provider_ref,omitempty"`
	AuthorizationID string            `json:"authorization_id,omitempty" yaml:"authorization_id,omitempty"`
	Status          DeploymentStatus  `json:"status" yaml:"status"`
	URL             string            `json:"url,omitempty" yaml:"url,omitempty"`
	Message         string            `json:"message,omitempty" yaml:"message,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at" yaml:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at" yaml:"updated_at"`
}

type Journal struct {
	Schema         string                         `json:"schema"`
	Plans          map[string]Plan                `json:"plans"`
	Authorizations map[string]BudgetAuthorization `json:"authorizations"`
	Deployments    map[string]Deployment          `json:"deployments"`
}
