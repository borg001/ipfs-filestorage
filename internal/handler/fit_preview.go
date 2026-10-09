package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

const (
	fitPreviewVariant   = "fit_1024"
	fitPreviewMaxSource = 100 << 20
)

// ensureFitPreview generates a rendition once for both existing and new
// bundles. The IPFS CID is persisted on the instance's data volume, alongside
// the existing variant overrides, so restarts do not trigger recomputation.
func (h *Handler) ensureFitPreview(ctx context.Context, cid string, manifest bundle.Manifest) (string, error) {
	key := store.VariantOverrideKey(cid, fitPreviewVariant)
	if previewCID, ok := h.variantOverrides.Get(key); ok {
		return previewCID, nil
	}
	value, err, _ := h.fitPreviewGroup.Do(key, func() (interface{}, error) {
		if previewCID, ok := h.variantOverrides.Get(key); ok {
			return previewCID, nil
		}
		if h.imageProcessor == nil || h.variantOverrides == nil || manifest.Type != "image" {
			return nil, fmt.Errorf("image preview is not configured")
		}
		reader, err := h.cluster.ClusterTryFetchPath(ctx, cid, manifest.Original.BundlePath)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		source, err := io.ReadAll(io.LimitReader(reader, fitPreviewMaxSource+1))
		if err != nil {
			return nil, err
		}
		if len(source) > fitPreviewMaxSource {
			return nil, fmt.Errorf("source image exceeds preview limit")
		}
		preview, err := h.imageProcessor.FitPreview(ctx, source, 1024)
		if err != nil {
			return nil, err
		}
		added, err := h.cluster.ClusterAdd(ctx, fitPreviewVariant+".jpg", bytes.NewReader(preview))
		if err != nil {
			return nil, err
		}
		if err := h.variantOverrides.Set(key, added.CID); err != nil {
			return nil, err
		}
		h.replicateAsync(added.CID)
		return added.CID, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}
