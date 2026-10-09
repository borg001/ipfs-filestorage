package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// uploadBudget bounds what one session uploads in a day on this instance.
// Storage does not know the account behind a session, only its token, and
// uploads were unmetered: any signed-in session could fill the disk. A nil
// budget allows everything.
type uploadBudget struct {
	mu    sync.Mutex
	limit int64
	day   string
	used  map[string]int64
}

func newUploadBudget(limit int64) *uploadBudget {
	if limit <= 0 {
		return nil
	}
	return &uploadBudget{limit: limit, used: map[string]int64{}}
}

// take records size bytes for the session and answers whether they fit.
func (b *uploadBudget) take(key string, size int64) bool {
	if b == nil || key == "" {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	today := time.Now().UTC().Format("2006-01-02")
	if b.day != today {
		b.day, b.used = today, map[string]int64{}
	}
	if b.used[key]+size > b.limit {
		return false
	}
	b.used[key] += size
	return true
}

// uploadSessionKey names the session an upload comes from without keeping
// its token.
func uploadSessionKey(r *http.Request) string {
	token := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	} else if cookie, err := r.Cookie("iamfree_media_token"); err == nil {
		token = cookie.Value
	} else {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:16])
}

// transcodeSlots is how many videos this instance transcodes at once. Every
// upload started its own ffmpeg with no limit, so a few long videos took both
// cores and everything else waited.
var transcodeSlots = make(chan struct{}, 2)

// errTranscodeBusy is an upload that waited too long for a slot.
var errTranscodeBusy = errors.New("transcoding is busy")

const (
	transcodeQueueWait = 5 * time.Minute
	transcodeTimeout   = 20 * time.Minute
)

// acquireTranscodeSlot waits for a free slot, as long as the upload is alive
// and not longer than transcodeQueueWait.
func acquireTranscodeSlot(ctx context.Context) (func(), error) {
	timer := time.NewTimer(transcodeQueueWait)
	defer timer.Stop()
	select {
	case transcodeSlots <- struct{}{}:
		return func() { <-transcodeSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errTranscodeBusy
	}
}
