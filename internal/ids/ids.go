package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var fallbackCounter atomic.Uint64

func New(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Entropy failure should not crash an in-progress deployment journal.
		// The timestamp/counter fallback is process-unique and intentionally
		// obvious rather than pretending to be cryptographically random.
		return fmt.Sprintf("%s_fallback_%x_%x", prefix, time.Now().UTC().UnixNano(), fallbackCounter.Add(1))
	}
	return prefix + "_" + hex.EncodeToString(b)
}
