package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

const fitPreviewMaxSource = 100 << 20

func (h *Handler) configuredPreview(variant string) (config.ImageVariant, bool) {
	if h.cfg == nil || !h.cfg.Image.ProcessingEnabled {
		return config.ImageVariant{}, false
	}
	for _, item := range h.cfg.Image.Variants {
		if item.Key == variant && item.Width > 0 && item.Height > 0 {
			return item, true
		}
	}
	return config.ImageVariant{}, false
}

// ensureConfiguredPreview generates a rendition once for both existing and new
// bundles. The IPFS CID is persisted on the instance's data volume, alongside
// the existing variant overrides, so restarts do not trigger recomputation.
func (h *Handler) ensureConfiguredPreview(ctx context.Context, cid string, manifest bundle.Manifest, variant, sourceVariant string, crop *deliveryCrop) (string, error) {
	size, ok := h.configuredPreview(variant)
	if !ok {
		return "", fmt.Errorf("unsupported configured preview %q", variant)
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
	// A redrawn privacy mask changes the source CID. Its resized rendition must
	// use a new key so an old preview cannot outlive the replacement mask.
	cacheVariant := fmt.Sprintf("preview-v2/%s/%dx%d/%s/%s/q%d", variant, size.Width, size.Height, size.Mode, h.cfg.Image.ResizePolicy, h.cfg.Image.JPEGQuality)
	if sourceVariant != "" {
		cacheVariant += "/" + sourceVariant
		if sourceCID != "" {
			cacheVariant += "/" + sourceCID
		}
	}
	// A framed avatar is its own rendition: the frame is part of its key.
	if crop != nil {
		cacheVariant += "/crop-" + crop.key()
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
		var preview []byte
		if crop != nil {
			preview, err = h.imageProcessor.CroppedPreview(ctx, source, size, crop.X, crop.Y, crop.W, crop.H)
		} else {
			preview, err = h.imageProcessor.ConfiguredPreview(ctx, source, size)
		}
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
