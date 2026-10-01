package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/imageproc"
	"github.com/borg001/ipfs-filestorage/internal/middleware"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

// A face mask is computed once, at upload, and kept with the photo. When the
// way masks are drawn changes, photos already uploaded keep the old masks.
// HandleRefreshFaceMasks computes them again from the originals with the
// current code and serves the new ones in their place: the photo keeps its
// address, only its mask is new.

const (
	faceMaskRefreshMaxItems  = 50
	faceMaskRefreshMaxSource = 100 << 20
)

type faceMaskRefreshRequest struct {
	// Bundles are photos stored as bundles; their blur_faces variant is redone.
	Bundles []string `json:"bundles"`
	// Posters are video posters: Source is the poster as extracted, Mask the
	// face-masked rendition made from it at upload.
	Posters []faceMaskPoster `json:"posters"`
}

type faceMaskPoster struct {
	Source string `json:"source"`
	Mask   string `json:"mask"`
}

type faceMaskRefreshResult struct {
	CID    string `json:"cid"`
	Status string `json:"status"`
	NewCID string `json:"new_cid,omitempty"`
	Error  string `json:"error,omitempty"`
}

// HandleRefreshFaceMasks serves POST /admin/face-masks/refresh. Only the
// platform calls it, with its API key: it writes to the cluster.
func (h *Handler) HandleRefreshFaceMasks(w http.ResponseWriter, r *http.Request) {
	if middleware.UserIDFromContext(r.Context()) != "api-key" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Refreshing face masks is not allowed"})
		return
	}
	var request faceMaskRefreshRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	if total := len(request.Bundles) + len(request.Posters); total == 0 || total > faceMaskRefreshMaxItems {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Send 1 to %d items at a time", faceMaskRefreshMaxItems)})
		return
	}
	results := make([]faceMaskRefreshResult, 0, len(request.Bundles)+len(request.Posters))
	for _, cid := range request.Bundles {
		results = append(results, h.refreshBundleFaceMask(r.Context(), cid))
	}
	for _, poster := range request.Posters {
		results = append(results, h.refreshPosterFaceMask(r.Context(), poster))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

func (h *Handler) refreshBundleFaceMask(ctx context.Context, cid string) faceMaskRefreshResult {
	result := faceMaskRefreshResult{CID: cid}
	if err := validateCID(cid); err != nil {
		return failedFaceMask(result, "invalid", err)
	}
	manifest, err := h.readManifest(ctx, cid)
	if err != nil {
		return failedFaceMask(result, "not_bundle", err)
	}
	variant, ok := manifest.Variants[config.PrivacyFaceBlurVariantKey]
	if !ok {
		result.Status = "no_mask"
		return result
	}
	reader, err := h.cluster.ClusterTryFetchPath(ctx, cid, manifest.Original.BundlePath)
	if err != nil {
		return failedFaceMask(result, "source_missing", err)
	}
	mask, status, err := h.redrawFaceMask(ctx, reader, manifest.Original.ContentType)
	if err != nil || status != "" {
		return failedFaceMask(result, status, err)
	}
	// The mask is served under the type the bundle declared for it.
	if variant.ContentType != "" && mask.ContentType != variant.ContentType {
		return failedFaceMask(result, "format_changed", fmt.Errorf("%s is now %s", variant.ContentType, mask.ContentType))
	}
	return h.storeFaceMask(ctx, result, store.VariantOverrideKey(cid, config.PrivacyFaceBlurVariantKey), mask)
}

func (h *Handler) refreshPosterFaceMask(ctx context.Context, poster faceMaskPoster) faceMaskRefreshResult {
	result := faceMaskRefreshResult{CID: poster.Mask}
	if err := validateCID(poster.Source); err != nil {
		return failedFaceMask(result, "invalid", err)
	}
	if err := validateCID(poster.Mask); err != nil {
		return failedFaceMask(result, "invalid", err)
	}
	reader, err := h.fetchVideoAsset(ctx, poster.Source)
	if err != nil {
		return failedFaceMask(result, "source_missing", err)
	}
	mask, status, err := h.redrawFaceMask(ctx, reader, "image/jpeg")
	if err != nil || status != "" {
		return failedFaceMask(result, status, err)
	}
	return h.storeFaceMask(ctx, result, store.VariantOverrideKey(poster.Mask, ""), mask)
}

// redrawFaceMask draws the face mask of a picture with the current code, the
// same way an upload does.
func (h *Handler) redrawFaceMask(ctx context.Context, reader io.ReadCloser, contentType string) (imageproc.Variant, string, error) {
	defer reader.Close()
	source, err := io.ReadAll(io.LimitReader(reader, faceMaskRefreshMaxSource+1))
	if err != nil {
		return imageproc.Variant{}, "source_missing", err
	}
	if len(source) > faceMaskRefreshMaxSource {
		return imageproc.Variant{}, "too_large", fmt.Errorf("source exceeds %d bytes", faceMaskRefreshMaxSource)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil {
		return imageproc.Variant{}, "not_image", err
	}
	if int64(cfg.Width)*int64(cfg.Height) > imageproc.MaxDecodePixels {
		return imageproc.Variant{}, "too_large", imageproc.ErrImageTooLarge
	}
	variants, err := h.imageProcessor.ProcessPrivacy(ctx, source, contentType)
	if err != nil {
		return imageproc.Variant{}, "failed", err
	}
	for _, variant := range variants {
		if variant.Key == config.PrivacyFaceBlurVariantKey {
			return variant, "", nil
		}
	}
	return imageproc.Variant{}, "not_image", fmt.Errorf("no face mask was drawn")
}

func (h *Handler) storeFaceMask(ctx context.Context, result faceMaskRefreshResult, key string, mask imageproc.Variant) faceMaskRefreshResult {
	added, err := h.cluster.ClusterAdd(ctx, mask.Filename, bytes.NewReader(mask.Data))
	if err != nil {
		return failedFaceMask(result, "add_failed", err)
	}
	if err := h.variantOverrides.Set(key, added.CID); err != nil {
		return failedFaceMask(result, "save_failed", err)
	}
	result.Status = "refreshed"
	result.NewCID = added.CID
	return result
}

func failedFaceMask(result faceMaskRefreshResult, status string, err error) faceMaskRefreshResult {
	result.Status = status
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

// overriddenFile is the file that now stands in for cid, or cid itself.
func (h *Handler) overriddenFile(cid string) string {
	if replacement, ok := h.variantOverrides.Get(store.VariantOverrideKey(cid, "")); ok {
		return replacement
	}
	return cid
}
