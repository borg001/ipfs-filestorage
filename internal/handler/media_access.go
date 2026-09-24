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

type mediaDeliveryMode string

const (
	mediaDeliveryOriginal  mediaDeliveryMode = "original"
	mediaDeliveryBlur      mediaDeliveryMode = "blur"
	mediaDeliveryBlurFaces mediaDeliveryMode = "blur_faces"
	// mediaDeliveryDeny is a file placed somewhere and reachable by this
	// viewer through none of those places.
	mediaDeliveryDeny mediaDeliveryMode = "deny"
)

// errMediaForbidden is a refusal by the policy, answered 403 rather than as
// an outage.
var errMediaForbidden = errors.New("media access denied")

type mediaAccessResolver struct {
	cidEndpoint  *url.URL
	linkEndpoint *url.URL
	client       *http.Client
}

type mediaDeliveryDecision struct {
	Mode           mediaDeliveryMode
	Managed        bool
	ReplacementCID string
	SourceCID      string
	PosterCID      string
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

// Resolve returns managed=false for files placed nowhere (chat attachments),
// the policy's answer for a placed file, and errMediaForbidden when the viewer
// may reach none of its places.
//
// A media_link in the query speaks only for its own file. The decision used
// to be taken for the link and applied to whatever CID the path named, so a
// viewer's own public link served the original of any CID he knew - a private
// gallery photo, a hidden face. A link that is not this file's is set aside
// and the file is judged by its CID alone.
func (r *mediaAccessResolver) Resolve(ctx context.Context, source *http.Request, cid string) (mediaDeliveryDecision, error) {
	decision, err := r.resolve(ctx, source, cid, "")
	// Only the link's own file counts: its poster and metadata are written by
	// whoever registered the asset, so they could name anyone's CID.
	if err == nil && cid != "" && source != nil && strings.TrimSpace(source.URL.Query().Get("media_link")) != "" &&
		decision.Managed && decision.SourceCID != cid {
		stripped := source.Clone(ctx)
		query := stripped.URL.Query()
		query.Del("media_link")
		stripped.URL.RawQuery = query.Encode()
		decision, err = r.resolve(ctx, stripped, cid, "")
	}
	if err == nil && decision.Mode == mediaDeliveryDeny {
		return mediaDeliveryDecision{}, errMediaForbidden
	}
	return decision, err
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
		return mediaDeliveryDecision{}, fmt.Errorf("media link is unavailable")
	}
	return decision, nil
}

func (r *mediaAccessResolver) resolve(ctx context.Context, source *http.Request, cid, requestedMediaLink string) (mediaDeliveryDecision, error) {
	if r == nil {
		return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
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
			query.Set("search", cid)
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
		return mediaDeliveryDecision{Mode: mediaDeliveryOriginal}, nil
	}

	decision := mediaDeliveryDecision{Mode: mediaDeliveryOriginal, Managed: true}
	for _, row := range payload.Rows {
		candidate, ok := responseString(row["delivery_mode"])
		if !ok {
			return mediaDeliveryDecision{}, fmt.Errorf("media policy response has no delivery_mode")
		}
		switch mediaDeliveryMode(candidate) {
		case mediaDeliveryDeny:
			decision.Mode = mediaDeliveryDeny
		case mediaDeliveryBlur:
			if decision.Mode != mediaDeliveryDeny {
				decision.Mode = mediaDeliveryBlur
			}
		case mediaDeliveryBlurFaces:
			if decision.Mode != mediaDeliveryBlur && decision.Mode != mediaDeliveryDeny {
				decision.Mode = mediaDeliveryBlurFaces
			}
		case mediaDeliveryOriginal:
		default:
			return mediaDeliveryDecision{}, fmt.Errorf("media policy returned unsupported mode %q", candidate)
		}
		if replacement := mediaPosterReplacement(row["metadata"], cid, mediaDeliveryMode(candidate)); replacement != "" && decision.Mode != mediaDeliveryOriginal {
			decision.ReplacementCID = replacement
		}
		if decision.SourceCID == "" {
			decision.SourceCID = mediaStorageCID(row["storage_uri"])
		}
		if decision.PosterCID == "" {
			decision.PosterCID = mediaStorageCID(row["poster_uri"])
		}
	}
	if decision.Mode != mediaDeliveryOriginal {
		for _, row := range payload.Rows {
			posterCID := decision.PosterCID
			if posterCID == "" {
				posterCID = cid
			}
			if replacement := mediaPosterReplacement(row["metadata"], posterCID, decision.Mode); replacement != "" {
				decision.ReplacementCID = replacement
				break
			}
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

func (h *Handler) resolveMediaDeliveryLink(r *http.Request, mediaLink string) (mediaDeliveryDecision, error) {
	if h.mediaAccess == nil {
		return mediaDeliveryDecision{}, fmt.Errorf("media access resolver is not configured")
	}
	return h.mediaAccess.ResolveLink(r.Context(), r, mediaLink)
}

// writeMediaResolveError answers a failed policy lookup: a refusal as 403, an
// outage as 503.
func writeMediaResolveError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMediaForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Media is not available to you"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
}

// protectedMediaCacheControl keeps every answer out of shared caches: an
// unmanaged file is a chat or support attachment, private to its thread even
// though the policy does not govern it.
func protectedMediaCacheControl(managed bool) string {
	if managed {
		return "private, no-store"
	}
	return "private, max-age=31536000, immutable"
}
