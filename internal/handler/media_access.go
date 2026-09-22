package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

var errMediaAccessDenied = errors.New("media access denied")

func writeMediaAccessError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	if errors.Is(err, errMediaAccessDenied) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Media access denied"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
}

type mediaDeliveryMode string

const (
	mediaDeliveryOriginal  mediaDeliveryMode = "original"
	mediaDeliveryBlur      mediaDeliveryMode = "blur"
	mediaDeliveryBlurFaces mediaDeliveryMode = "blur_faces"
)

type mediaAccessResolver struct {
	cidEndpoint  *url.URL
	linkEndpoint *url.URL
	client       *http.Client
}

type mediaDeliveryDecision struct {
	Mode          mediaDeliveryMode
	Managed       bool
	SourceCID     string
	SourceIsVideo bool
	PosterCID     string
}

func newMediaAccessResolver(cfg config.MediaAccessConfig) *mediaAccessResolver {
	cidEndpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || cidEndpoint.Scheme == "" || cidEndpoint.Host == "" {
		return nil
	}
	linkEndpoint := cidEndpoint
	if configured := strings.TrimSpace(cfg.LinkURL); configured != "" {
		linkEndpoint, err = url.Parse(configured)
		if err != nil || linkEndpoint.Scheme == "" || linkEndpoint.Host == "" {
			return nil
		}
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	return &mediaAccessResolver{cidEndpoint: cidEndpoint, linkEndpoint: linkEndpoint, client: &http.Client{Timeout: timeout}}
}

// Resolve requires an explicit policy for every resource, including attachments.
// An empty response is a denial, never evidence that a resource is public.
func (r *mediaAccessResolver) Resolve(ctx context.Context, source *http.Request, cid string) (mediaDeliveryDecision, error) {
	return r.resolve(ctx, source, cid, "")
}

// ResolveLink resolves an opaque media_links ID through the internal API.
// The storage server is then the only process that sees the backing CID.
func (r *mediaAccessResolver) ResolveLink(ctx context.Context, source *http.Request, mediaLink string) (mediaDeliveryDecision, error) {
	if strings.TrimSpace(mediaLink) == "" {
		return mediaDeliveryDecision{}, fmt.Errorf("media link is required")
	}
	decision, err := r.resolve(ctx, source, "", mediaLink)
	if err != nil {
		return mediaDeliveryDecision{}, err
	}
	if !decision.Managed || decision.SourceCID == "" {
		return mediaDeliveryDecision{}, errMediaAccessDenied
	}
	return decision, nil
}

func (r *mediaAccessResolver) resolve(ctx context.Context, source *http.Request, cid, requestedMediaLink string) (mediaDeliveryDecision, error) {
	if r == nil {
		return mediaDeliveryDecision{}, fmt.Errorf("media policy is not configured")
	}
	endpoint := *r.cidEndpoint
	query := endpoint.Query()
	mediaLink := requestedMediaLink
	if source != nil && mediaLink == "" {
		mediaLink = strings.TrimSpace(source.URL.Query().Get("media_link"))
	}
	if mediaLink != "" {
		endpoint = *r.linkEndpoint
		// The opaque link is the policy resource. A standard generator view
		// avoids the list action's second COUNT query on every file request.
		endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/view/id/" + url.PathEscape(mediaLink)
		query.Del("search")
		query.Del("size")
		query.Del("media_link")
	} else {
		if cid != "" {
			searchCID := cid
			if source != nil && source.URL.Query().Get("media_root") != "" {
				searchCID = source.URL.Query().Get("media_root")
			}
			query.Set("search", searchCID)
		}
		query.Set("size", "2")
	}
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
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized {
		return mediaDeliveryDecision{}, errMediaAccessDenied
	}
	// The current generator View encodes an absent/filtered record as 400.
	// Only this exact domain error is a denial; SQL/transport errors remain 503.
	if response.StatusCode == http.StatusBadRequest {
		var failure struct {
			Message string `json:"message"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&failure) == nil && failure.Message == "Record not found" {
			return mediaDeliveryDecision{}, errMediaAccessDenied
		}
	}
	if response.StatusCode != http.StatusOK {
		return mediaDeliveryDecision{}, fmt.Errorf("media policy returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Rows []map[string]interface{} `json:"rows"`
		Item map[string]interface{}   `json:"item"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return mediaDeliveryDecision{}, fmt.Errorf("decode media policy: %w", err)
	}
	if payload.Item != nil {
		payload.Rows = []map[string]interface{}{payload.Item}
	}
	if len(payload.Rows) == 0 {
		return mediaDeliveryDecision{}, errMediaAccessDenied
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
		if decision.SourceCID == "" {
			decision.SourceCID = mediaStorageCID(row["storage_uri"])
			sourceURI, _ := responseString(row["storage_uri"])
			decision.SourceIsVideo = strings.HasPrefix(sourceURI, "video://")
		}
		if decision.PosterCID == "" {
			decision.PosterCID = mediaStorageCID(row["poster_uri"])
		}
	}
	return decision, nil
}

func mediaStorageCID(value interface{}) string {
	uri, ok := responseString(value)
	if !ok {
		return ""
	}
	for _, prefix := range []string{"ipfs://", "video://"} {
		if cid := strings.TrimPrefix(uri, prefix); cid != uri {
			return strings.TrimSpace(cid)
		}
	}
	return ""
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
		if h.cfg != nil && h.cfg.MediaAccess.AllowUnmanaged && h.cfg.MediaAccess.URL == "" && h.cfg.MediaAccess.LinkURL == "" {
			return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
		}
		return mediaDeliveryDecision{}, fmt.Errorf("media policy is not configured")
	}
	decision, err := h.mediaAccess.Resolve(r.Context(), r, cid)
	if err != nil {
		return mediaDeliveryDecision{}, err
	}
	if decision.Managed {
		if err := h.validateMediaResource(r, cid, decision); err != nil {
			return mediaDeliveryDecision{}, err
		}
	}
	return decision, nil
}

func (h *Handler) resolveMediaDeliveryLink(r *http.Request, mediaLink string) (mediaDeliveryDecision, error) {
	if h.mediaAccess == nil {
		return mediaDeliveryDecision{}, fmt.Errorf("media access resolver is not configured")
	}
	return h.mediaAccess.ResolveLink(r.Context(), r, mediaLink)
}

func protectedMediaCacheControl(managed bool) string {
	if managed {
		return "private, no-store"
	}
	return "public, max-age=31536000, immutable"
}
