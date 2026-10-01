package handler

import (
	"net/http"
	"strings"

	cidlib "github.com/ipfs/go-cid"
)

// A policy for link A must never authorize bytes from link B. HLS children
// belong to the immutable server-produced playlists, not to a list of CIDs
// supplied by the browser in media metadata.
func (h *Handler) validateMediaResource(r *http.Request, cid string, decision mediaDeliveryDecision) error {
	if decision.SourceCID == "" || h.unpinStore.Has(decision.SourceCID) {
		return errMediaAccessDenied
	}
	if strings.HasPrefix(r.URL.Path, "/file/") || !strings.HasPrefix(r.URL.Path, "/stream/segment/") {
		if sameMediaCID(cid, decision.SourceCID) {
			return nil
		}
		return errMediaAccessDenied
	}
	if sameMediaCID(cid, decision.SourceCID) && decision.Mode == mediaDeliveryOriginal {
		return nil
	}
	if !decision.SourceIsVideo {
		return errMediaAccessDenied
	}
	master, err := h.readLinkedMaster(r.Context(), decision)
	if err != nil {
		return err
	}
	if _, err := videoPosterCID(master, cid, decision.Mode); err == nil {
		return nil
	}
	if decision.Mode != mediaDeliveryOriginal {
		return errMediaAccessDenied
	}
	// The encoder produces a small fixed set of qualities. Bound traversal of
	// malformed manifests; opaque indexed URLs avoid this compatibility scan.
	for i := 0; i < 32; i++ {
		playlistCID, err := linkedMasterPlaylistCID(master, i)
		if err != nil {
			return errMediaAccessDenied
		}
		if sameMediaCID(cid, playlistCID) {
			return nil
		}
		if h.unpinStore.Has(playlistCID) {
			continue
		}
		playlist, err := h.readVideoAsset(r.Context(), playlistCID)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(playlist, "\n") {
			value := strings.TrimSpace(line)
			if strings.HasPrefix(value, "#") {
				value, _ = playlistURI(value)
			}
			if value == "" {
				continue
			}
			childCID, _, err := playlistReferenceCID(value)
			if err != nil {
				return errMediaAccessDenied
			}
			if sameMediaCID(childCID, cid) {
				return nil
			}
		}
	}
	return errMediaAccessDenied
}

func (h *Handler) resolveVideoPoster(r *http.Request, decision mediaDeliveryDecision, requested string) (string, error) {
	if !decision.SourceIsVideo {
		return "", errMediaAccessDenied
	}
	master, err := h.readLinkedMaster(r.Context(), decision)
	if err != nil {
		return "", err
	}
	return videoPosterCID(master, requested, decision.Mode)
}

// The master binds both the original poster and its protected rendition to
// the video. Client-editable poster aliases cannot select the returned bytes.
func videoPosterCID(master, requested string, mode mediaDeliveryMode) (string, error) {
	type poster struct{ cid, size, variant string }
	posters := []poster{}
	for _, line := range strings.Split(master, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "#EXT-X-IAMFREE-POSTER:"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		uri, ok := playlistURI(line)
		if !ok {
			return "", errMediaAccessDenied
		}
		cid, ext, err := playlistReferenceCID(uri)
		if err != nil || ext != ".jpg" {
			return "", errMediaAccessDenied
		}
		p := poster{cid: cid}
		for _, attr := range strings.Split(strings.TrimPrefix(line, prefix), ",") {
			if value, ok := strings.CutPrefix(attr, "SIZE="); ok {
				p.size = value
			}
			if value, ok := strings.CutPrefix(attr, "VARIANT="); ok {
				p.variant = value
			}
		}
		if p.size == "" {
			return "", errMediaAccessDenied
		}
		posters = append(posters, p)
	}
	for _, original := range posters {
		if original.variant != "" || (requested != "" && !sameMediaCID(original.cid, requested)) {
			continue
		}
		if mode == mediaDeliveryOriginal {
			return original.cid, nil
		}
		for _, protected := range posters {
			if protected.size == original.size && protected.variant == string(mode) {
				return protected.cid, nil
			}
		}
	}
	return "", errMediaAccessDenied
}

func sameMediaCID(left, right string) bool {
	if left == right {
		return true
	}
	a, err := cidlib.Decode(left)
	if err != nil {
		return false
	}
	b, err := cidlib.Decode(right)
	if err != nil {
		return false
	}
	return cidlib.NewCidV1(a.Type(), a.Hash()).Equals(cidlib.NewCidV1(b.Type(), b.Hash()))
}
