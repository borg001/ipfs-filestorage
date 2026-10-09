package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

const fitPreviewMaxSource = 100 << 20

var fitPreviewSizes = map[string]int{"fit_480": 480, "fit_1024": 1024}

// ensureFitPreview generates a rendition once for both existing and new
// bundles. The IPFS CID is persisted on the instance's data volume, alongside
// the existing variant overrides, so restarts do not trigger recomputation.
func (h *Handler) ensureFitPreview(ctx context.Context, cid string, manifest bundle.Manifest, variant, sourceVariant string) (string, error) {
	maxSide, ok := fitPreviewSizes[variant]
	if !ok {
		return "", fmt.Errorf("unsupported fit preview %q", variant)
	}
	if h.imageProcessor == nil || h.variantOverrides == nil || manifest.Type != "image" {
		return "", fmt.Errorf("image preview is not configured")
	}
	sourcePath := manifest.Original.BundlePath
	sourceCID := ""
	if sourceVariant != "" {
		asset, ok := manifest.Variants[sourceVariant]
		if !ok {
			return "", fmt.Errorf("source variant %q is unavailable", sourceVariant)
		}
		sourcePath = asset.BundlePath
		sourceCID, _ = h.variantOverrides.Get(store.VariantOverrideKey(cid, sourceVariant))
	}
	// A redrawn privacy mask changes the source CID. Its fit rendition must
	// use a new key so an old preview cannot outlive the replacement mask.
	cacheVariant := variant
	if sourceVariant != "" {
		cacheVariant += "/" + sourceVariant
		if sourceCID != "" {
			cacheVariant += "/" + sourceCID
		}
	}
	key := store.VariantOverrideKey(cid, cacheVariant)
	if previewCID, ok := h.variantOverrides.Get(key); ok {
		return previewCID, nil
	}
	value, err, _ := h.fitPreviewGroup.Do(key, func() (interface{}, error) {
		if previewCID, ok := h.variantOverrides.Get(key); ok {
			return previewCID, nil
		}
		var reader io.ReadCloser
		var err error
		if sourceCID != "" {
			reader, err = h.cluster.ClusterTryFetch(ctx, sourceCID)
		} else {
			reader, err = h.cluster.ClusterTryFetchPath(ctx, cid, sourcePath)
		}
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
		preview, err := h.imageProcessor.FitPreview(ctx, source, maxSide)
		if err != nil {
			return nil, err
		}
		added, err := h.cluster.ClusterAdd(ctx, variant+".jpg", bytes.NewReader(preview))
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
