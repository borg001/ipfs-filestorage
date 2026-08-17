package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

type mediaDeliveryMode string

const (
	mediaDeliveryOriginal  mediaDeliveryMode = "original"
	mediaDeliveryBlur      mediaDeliveryMode = "blur"
	mediaDeliveryBlurFaces mediaDeliveryMode = "blur_faces"
)

type mediaAccessResolver struct {
	endpoint *url.URL
	client   *http.Client
}

type mediaDeliveryDecision struct {
	Mode           mediaDeliveryMode
	Managed        bool
	ReplacementCID string
}

func newMediaAccessResolver(cfg config.MediaAccessConfig) *mediaAccessResolver {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	return &mediaAccessResolver{endpoint: endpoint, client: &http.Client{Timeout: timeout}}
}

// Resolve returns managed=false for files not owned by the profile gallery.
// This retains normal storage behavior for chat attachments and every future
// non-profile media use while making managed content fail closed on API errors.
func (r *mediaAccessResolver) Resolve(ctx context.Context, source *http.Request, cid string) (mediaDeliveryDecision, error) {
	if r == nil {
		return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
	}
	endpoint := *r.endpoint
	query := endpoint.Query()
	query.Set("search", cid)
	query.Set("size", "2")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return mediaDeliveryDecision{}, fmt.Errorf("create media policy request: %w", err)
	}
	forwardMediaAuthorization(req, source)

	response, err := r.client.Do(req)
	if err != nil {
		return mediaDeliveryDecision{}, fmt.Errorf("request media policy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return mediaDeliveryDecision{}, fmt.Errorf("media policy returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Rows []map[string]interface{} `json:"rows"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return mediaDeliveryDecision{}, fmt.Errorf("decode media policy: %w", err)
	}
	if len(payload.Rows) == 0 {
		return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
	}

	decision := mediaDeliveryDecision{Mode: mediaDeliveryOriginal, Managed: true}
	for _, row := range payload.Rows {
		candidate, ok := responseString(row["delivery_mode"])
		if !ok {
			return mediaDeliveryDecision{}, fmt.Errorf("media policy response has no delivery_mode")
		}
		switch mediaDeliveryMode(candidate) {
		case mediaDeliveryBlur:
			decision.Mode = mediaDeliveryBlur
		case mediaDeliveryBlurFaces:
			if decision.Mode != mediaDeliveryBlur {
				decision.Mode = mediaDeliveryBlurFaces
			}
		case mediaDeliveryOriginal:
		default:
			return mediaDeliveryDecision{}, fmt.Errorf("media policy returned unsupported mode %q", candidate)
		}
		if replacement := mediaPosterReplacement(row["metadata"], cid, mediaDeliveryMode(candidate)); replacement != "" && decision.Mode != mediaDeliveryOriginal {
			decision.ReplacementCID = replacement
		}
	}
	if decision.Mode != mediaDeliveryOriginal {
		for _, row := range payload.Rows {
			if replacement := mediaPosterReplacement(row["metadata"], cid, decision.Mode); replacement != "" {
				decision.ReplacementCID = replacement
				break
			}
		}
	}
	return decision, nil
}

func mediaPosterReplacement(value interface{}, originalCID string, mode mediaDeliveryMode) string {
	metadata, ok := responseMap(value)
	if !ok {
		return ""
	}
	aliases, ok := responseMap(metadata["poster_aliases"])
	if !ok {
		return ""
	}
	values, ok := responseMap(aliases[originalCID])
	if !ok {
		return ""
	}
	replacement, _ := responseString(values[string(mode)])
	return replacement
}

func responseMap(value interface{}) (map[string]interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if nested, exists := typed["value"]; exists && len(typed) == 1 {
			return responseMap(nested)
		}
		return typed, true
	case string:
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil {
			return decoded, true
		}
	}
	return nil, false
}

func responseString(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case map[string]interface{}:
		return responseString(typed["value"])
	default:
		return "", false
	}
}

func forwardMediaAuthorization(target *http.Request, source *http.Request) {
	if source == nil {
		return
	}
	if authorization := source.Header.Get("Authorization"); authorization != "" {
		target.Header.Set("Authorization", authorization)
		return
	}
	if token := source.URL.Query().Get("token"); token != "" {
		target.Header.Set("Authorization", "Bearer "+token)
		return
	}
	if token := source.URL.Query().Get("access_token"); token != "" {
		target.Header.Set("Authorization", "Bearer "+token)
	}
}

func (h *Handler) resolveMediaDelivery(r *http.Request, cid string) (mediaDeliveryDecision, error) {
	if h.mediaAccess == nil {
		return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
	}
	return h.mediaAccess.Resolve(r.Context(), r, cid)
}

func protectedMediaCacheControl(managed bool) string {
	if managed {
		return "private, no-store"
	}
	return "public, max-age=31536000, immutable"
}
