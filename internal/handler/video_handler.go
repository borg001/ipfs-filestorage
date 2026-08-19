package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/video"
)

// VideoResponse — ответ POST /upload-video.
type VideoResponse struct {
	MasterCID         string                       `json:"master_cid"`
	VariantCIDs       map[string]string            `json:"variant_cids"`
	PosterCIDs        map[string]string            `json:"poster_cids,omitempty"`
	PrivacyPosterCIDs map[string]map[string]string `json:"privacy_poster_cids,omitempty"`
	StreamCIDs        []string                     `json:"stream_cids,omitempty"`
	PosterAliases     map[string]map[string]string `json:"poster_aliases,omitempty"`
	DurationSec       float64                      `json:"duration_sec"`
	Status            string                       `json:"status"`
}

// clusterAdder адаптирует ipfs.Clusterer к интерфейсу video.IPFSAdder.
type clusterAdder struct {
	cluster ipfs.Clusterer
}

func (c *clusterAdder) Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	return c.cluster.ClusterAdd(ctx, filename, r)
}

// HandleUploadVideo обрабатывает POST /upload-video.
func (h *Handler) HandleUploadVideo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(h.cfg.Video.MaxSizeBytes + (1 << 20)); err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "upload_form_invalid", nil)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "upload_missing_file", nil)
		return
	}
	defer file.Close()

	// Проверяем, что это видео по расширению
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	videoExts := map[string]bool{"mp4": true, "mov": true, "webm": true, "avi": true, "mkv": true}
	if !videoExts[ext] {
		writeUploadError(w, r, http.StatusBadRequest, "unsupported_video_format", map[string]any{
			"allowed_extensions": []string{"mp4", "mov", "webm", "avi", "mkv"},
		})
		return
	}

	// Проверяем свободное место на диске (3x от максимального размера)
	if err := os.MkdirAll(h.cfg.Video.TempDir, 0o755); err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_storage_unavailable", nil)
		return
	}
	if err := checkDiskSpace(h.cfg.Video.TempDir, h.cfg.Video.MaxSizeBytes*3); err != nil {
		writeUploadError(w, r, http.StatusInsufficientStorage, "upload_storage_unavailable", nil)
		return
	}

	// Сохраняем во временный файл с серверной проверкой размера
	tmpInput, err := os.CreateTemp(h.cfg.Video.TempDir, "video-input-*.mp4")
	if err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_storage_unavailable", nil)
		return
	}
	defer os.Remove(tmpInput.Name())
	defer tmpInput.Close()

	// Серверная проверка размера: ограничиваем чтение через LimitReader
	limitedReader := &countingReader{r: io.LimitReader(file, h.cfg.Video.MaxSizeBytes+1)}
	if _, err := tmpInput.ReadFrom(limitedReader); err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_failed", nil)
		return
	}

	// Если прочитано больше лимита — файл слишком большой
	if limitedReader.bytesRead > h.cfg.Video.MaxSizeBytes {
		writeUploadError(w, r, http.StatusRequestEntityTooLarge, "video_file_too_large", map[string]any{
			"max_bytes": h.cfg.Video.MaxSizeBytes,
		})
		return
	}

	ctx := r.Context()

	// Валидация видео (используем реальный размер, а не header.Size)
	validator := video.NewValidator(&h.cfg.Video)
	if err := validator.Validate(ctx, tmpInput.Name(), limitedReader.bytesRead); err != nil {
		var validationError *video.ValidationError
		if errors.As(err, &validationError) {
			details := map[string]any{}
			if validationError.MaxSizeBytes > 0 {
				details["max_bytes"] = validationError.MaxSizeBytes
			}
			if validationError.MaxDurationSec > 0 {
				details["max_duration_sec"] = validationError.MaxDurationSec
			}
			if validationError.ExpectedAspectRatio != "" {
				details["expected_aspect_ratio"] = validationError.ExpectedAspectRatio
			}
			writeUploadError(w, r, http.StatusBadRequest, validationError.Code, details)
			return
		}
		writeUploadError(w, r, http.StatusBadRequest, "video_metadata_invalid", nil)
		return
	}

	// Транскодирование
	outputDir, err := os.MkdirTemp(h.cfg.Video.TempDir, "video-output-*")
	if err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_storage_unavailable", nil)
		return
	}
	defer os.RemoveAll(outputDir)

	transcoder := video.NewTranscoder(&h.cfg.Video)
	result, err := transcoder.Transcode(ctx, tmpInput.Name(), outputDir)
	if err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_failed", nil)
		return
	}
	if err := h.buildVideoPrivacyPosters(ctx, outputDir); err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_failed", nil)
		return
	}

	// Загрузка в IPFS — через Clusterer → clusterAdder → video.IPFSAdder
	uploader := video.NewUploader(&clusterAdder{cluster: h.cluster})
	uploadResult, err := uploader.UploadDir(ctx, outputDir)
	if err != nil {
		writeUploadError(w, r, http.StatusServiceUnavailable, "upload_storage_unavailable", nil)
		return
	}

	// Репликация всех CID
	retryDelay := time.Duration(h.cfg.Pinning.RetryDelayMs) * time.Millisecond
	for _, cid := range uploadResult.AllCIDs {
		_ = h.cluster.ClusterReplicate(ctx, cid, h.cfg.Pinning.Retries, retryDelay)
	}

	// Сохраняем маппинг master_cid → all_cids для будущего группового удаления.
	h.unpinStore.TrackGroup(uploadResult.MasterCID, uploadResult.AllCIDs)

	resp := VideoResponse{
		MasterCID:         uploadResult.MasterCID,
		VariantCIDs:       uploadResult.VariantCIDs,
		PosterCIDs:        uploadResult.PosterCIDs,
		PrivacyPosterCIDs: uploadResult.PrivacyPosterCIDs,
		StreamCIDs:        uploadResult.AllCIDs,
		PosterAliases:     videoPosterAliases(uploadResult.PosterCIDs, uploadResult.PrivacyPosterCIDs),
		DurationSec:       result.Duration,
		Status:            "processing_done",
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleStreamMaster обрабатывает GET /stream/{cid}/master.m3u8.
func (h *Handler) HandleStreamMaster(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/stream/"), "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid stream URL"})
		return
	}
	cid := parts[0]

	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	decision, err := h.resolveMediaDelivery(r, cid)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
		return
	}
	if decision.Mode == mediaDeliveryBlur {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Private video is unavailable"})
		return
	}
	h.serveStreamMaster(w, r, cid, decision, "")
}

// HandleStreamLink serves opaque browser URLs. Every HLS level is addressed by
// a stable index within a media link, never by an IPFS CID:
//
//	/stream/link/{media_link}/master.m3u8
//	/stream/link/{media_link}/playlist/{playlist_index}.m3u8
//	/stream/link/{media_link}/segment/{playlist_index}/{asset_index}.m4s
//
// The storage process resolves indexes against the protected master playlist
// on each request, so a browser can neither learn nor reuse a backing CID.
func (h *Handler) HandleStreamLink(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/link/"), "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid media link stream URL"})
		return
	}
	decision, err := h.resolveMediaDeliveryLink(r, parts[0])
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
		return
	}
	switch {
	case len(parts) == 2 && parts[1] == "poster.jpg":
		// A private video never exposes its stream, but its existing blurred
		// poster is the safe visual replacement used by cards and stories.
		posterCID := decision.PosterCID
		if decision.ReplacementCID != "" {
			posterCID = decision.ReplacementCID
		}
		if err := validateCID(posterCID); err != nil || h.unpinStore.Has(posterCID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Poster not found"})
			return
		}
		h.serveStreamAsset(w, r, posterCID, "poster.jpg", decision)
	case decision.Mode == mediaDeliveryBlur:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Private video is unavailable"})
		return
	case len(parts) == 2 && parts[1] == "master.m3u8":
		h.serveLinkedStreamMaster(w, r, decision)
	case len(parts) == 3 && parts[1] == "playlist":
		playlistIndex, err := linkedStreamIndex(parts[2], ".m3u8")
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream playlist not found"})
			return
		}
		h.serveLinkedStreamPlaylist(w, r, playlistIndex, decision)
	case len(parts) == 4 && parts[1] == "segment":
		playlistIndex, err := linkedStreamIndex(parts[2], "")
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream segment not found"})
			return
		}
		extension := filepath.Ext(parts[3])
		assetIndex, err := linkedStreamIndex(strings.TrimSuffix(parts[3], extension), "")
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream segment not found"})
			return
		}
		h.serveLinkedStreamAsset(w, r, playlistIndex, assetIndex, extension, decision)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream asset not found"})
	}
}

func linkedStreamIndex(value, extension string) (int, error) {
	if extension != "" {
		if !strings.HasSuffix(value, extension) {
			return 0, fmt.Errorf("invalid extension")
		}
		value = strings.TrimSuffix(value, extension)
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("invalid stream index")
	}
	return index, nil
}

func (h *Handler) serveLinkedStreamMaster(w http.ResponseWriter, r *http.Request, decision mediaDeliveryDecision) {
	master, err := h.readLinkedMaster(r.Context(), decision)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	content, err := rewriteLinkedMasterPlaylist(master, playlistAuthSuffix(r.URL.Query()))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video playlist is unavailable"})
		return
	}
	writeLinkedPlaylist(w, content, decision)
}

func (h *Handler) serveLinkedStreamPlaylist(w http.ResponseWriter, r *http.Request, playlistIndex int, decision mediaDeliveryDecision) {
	master, err := h.readLinkedMaster(r.Context(), decision)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	playlistCID, err := linkedMasterPlaylistCID(master, playlistIndex)
	if err != nil || h.unpinStore.Has(playlistCID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream playlist not found"})
		return
	}
	playlist, err := h.readVideoAsset(r.Context(), playlistCID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream playlist not found"})
		return
	}
	content, err := rewriteLinkedVariantPlaylist(playlist, playlistIndex, playlistAuthSuffix(r.URL.Query()))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream playlist is unavailable"})
		return
	}
	writeLinkedPlaylist(w, content, decision)
}

func (h *Handler) serveLinkedStreamAsset(w http.ResponseWriter, r *http.Request, playlistIndex, assetIndex int, extension string, decision mediaDeliveryDecision) {
	master, err := h.readLinkedMaster(r.Context(), decision)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	playlistCID, err := linkedMasterPlaylistCID(master, playlistIndex)
	if err != nil || h.unpinStore.Has(playlistCID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream segment not found"})
		return
	}
	playlist, err := h.readVideoAsset(r.Context(), playlistCID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream playlist not found"})
		return
	}
	assetCID, assetExtension, err := linkedVariantAssetCID(playlist, assetIndex)
	if err != nil || h.unpinStore.Has(assetCID) || (extension != "" && extension != assetExtension) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Stream segment not found"})
		return
	}
	h.serveStreamAsset(w, r, assetCID, "asset"+assetExtension, decision)
}

func (h *Handler) readLinkedMaster(ctx context.Context, decision mediaDeliveryDecision) (string, error) {
	if err := validateCID(decision.SourceCID); err != nil || h.unpinStore.Has(decision.SourceCID) {
		return "", fmt.Errorf("invalid master")
	}
	return h.readVideoAsset(ctx, decision.SourceCID)
}

func (h *Handler) readVideoAsset(ctx context.Context, cid string) (string, error) {
	reader, err := h.fetchVideoAsset(ctx, cid)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeLinkedPlaylist(w http.ResponseWriter, content string, decision mediaDeliveryDecision) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	if decision.Managed {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, content)
}

func rewriteLinkedMasterPlaylist(content, authSuffix string) (string, error) {
	lines := strings.Split(content, "\n")
	playlistIndex := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if strings.Contains(trimmed, `URI="`) {
				rewritten, ok := replacePlaylistURI(line, "poster.jpg"+authSuffix)
				if !ok {
					return "", fmt.Errorf("invalid playlist URI")
				}
				lines[index] = rewritten
			}
			continue
		}
		_, extension, err := playlistReferenceCID(trimmed)
		if err != nil || extension != ".m3u8" {
			return "", fmt.Errorf("invalid master playlist reference")
		}
		lines[index] = "playlist/" + strconv.Itoa(playlistIndex) + ".m3u8" + authSuffix
		playlistIndex++
	}
	return strings.Join(lines, "\n"), nil
}

func rewriteLinkedVariantPlaylist(content string, playlistIndex int, authSuffix string) (string, error) {
	lines := strings.Split(content, "\n")
	assetIndex := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if strings.Contains(trimmed, `URI="`) {
				uri, ok := playlistURI(line)
				if !ok {
					return "", fmt.Errorf("invalid playlist URI")
				}
				_, extension, err := playlistReferenceCID(uri)
				if err != nil {
					return "", err
				}
				replacement := "../segment/" + strconv.Itoa(playlistIndex) + "/" + strconv.Itoa(assetIndex) + extension + authSuffix
				rewritten, ok := replacePlaylistURI(line, replacement)
				if !ok {
					return "", fmt.Errorf("invalid playlist URI")
				}
				lines[index] = rewritten
				assetIndex++
			}
			continue
		}
		_, extension, err := playlistReferenceCID(trimmed)
		if err != nil {
			return "", err
		}
		lines[index] = "../segment/" + strconv.Itoa(playlistIndex) + "/" + strconv.Itoa(assetIndex) + extension + authSuffix
		assetIndex++
	}
	return strings.Join(lines, "\n"), nil
}

func linkedMasterPlaylistCID(content string, wanted int) (string, error) {
	index := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		cid, extension, err := playlistReferenceCID(trimmed)
		if err != nil || extension != ".m3u8" {
			return "", fmt.Errorf("invalid master playlist reference")
		}
		if index == wanted {
			return cid, nil
		}
		index++
	}
	return "", fmt.Errorf("playlist index is unavailable")
}

func linkedVariantAssetCID(content string, wanted int) (string, string, error) {
	index := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		value := ""
		if strings.HasPrefix(trimmed, "#") {
			if uri, ok := playlistURI(line); ok {
				value = uri
			} else {
				continue
			}
		} else {
			value = trimmed
		}
		cid, extension, err := playlistReferenceCID(value)
		if err != nil {
			return "", "", err
		}
		if index == wanted {
			return cid, extension, nil
		}
		index++
	}
	return "", "", fmt.Errorf("segment index is unavailable")
}

func playlistReferenceCID(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", "", err
	}
	filename := filepath.Base(parsed.Path)
	extension := filepath.Ext(filename)
	cid := strings.TrimSuffix(filename, extension)
	if err := validateCID(cid); err != nil {
		return "", "", err
	}
	return cid, extension, nil
}

func playlistURI(line string) (string, bool) {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return "", false
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return "", false
	}
	return line[start : start+end], true
}

func replacePlaylistURI(line, replacement string) (string, bool) {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return line, false
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return line, false
	}
	end += start
	return line[:start] + replacement + line[end:], true
}

func (h *Handler) serveStreamMaster(w http.ResponseWriter, r *http.Request, cid string, decision mediaDeliveryDecision, mediaLink string) {

	ctx := r.Context()
	reader, err := h.fetchVideoAsset(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	if decision.Managed {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.WriteHeader(http.StatusOK)
	query := r.URL.Query()
	if mediaLink != "" {
		cloned := make(url.Values, len(query)+1)
		for key, values := range query {
			cloned[key] = append([]string(nil), values...)
		}
		query = cloned
		query.Set("media_link", mediaLink)
	}
	writePlaylist(w, reader, playlistAuthSuffix(query))
}

// HandleStreamSegment обрабатывает GET /stream/segment/{cid}.
func (h *Handler) HandleStreamSegment(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/stream/segment/")

	cid := path
	if dotIdx := strings.LastIndex(path, "."); dotIdx > 0 {
		cid = path[:dotIdx]
	}

	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}
	decision, err := h.resolveMediaDelivery(r, cid)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
		return
	}
	isPoster := strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg") || strings.EqualFold(filepath.Ext(path), ".webp") || strings.EqualFold(filepath.Ext(path), ".png")
	if decision.Mode == mediaDeliveryBlur && !isPoster {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Private video is unavailable"})
		return
	}
	if isPoster && decision.Mode != mediaDeliveryOriginal {
		if decision.ReplacementCID == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Protected poster is unavailable"})
			return
		}
		cid = decision.ReplacementCID
	}

	h.serveStreamAsset(w, r, cid, path, decision)
}

func (h *Handler) serveStreamAsset(w http.ResponseWriter, r *http.Request, cid, path string, decision mediaDeliveryDecision) {
	ctx := r.Context()
	reader, err := h.fetchVideoAsset(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}
	defer reader.Close()

	contentType := streamSegmentContentType(path)

	w.Header().Set("Content-Type", contentType)
	if decision.Managed {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	w.WriteHeader(http.StatusOK)
	if contentType == "application/vnd.apple.mpegurl" {
		writePlaylist(w, reader, playlistAuthSuffix(r.URL.Query()))
		return
	}
	io.Copy(w, reader)
}

func videoPosterAliases(posters map[string]string, privacy map[string]map[string]string) map[string]map[string]string {
	aliases := make(map[string]map[string]string)
	for size, original := range posters {
		if original == "" {
			continue
		}
		for variant, values := range privacy {
			if cid := values[size]; cid != "" {
				if aliases[original] == nil {
					aliases[original] = make(map[string]string)
				}
				aliases[original][variant] = cid
			}
		}
	}
	return aliases
}

func streamSegmentContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "video/mp4"
	}
}

func (h *Handler) fetchVideoAsset(ctx context.Context, cid string) (io.ReadCloser, error) {
	reader, err := h.cluster.ClusterTryFetch(ctx, cid)
	if err == nil {
		return reader, nil
	}
	return h.cluster.ClusterTryFetchPath(ctx, cid, bundle.OriginalFilename)
}

func playlistAuthSuffix(query url.Values) string {
	values := url.Values{}
	key := "token"
	token := query.Get(key)
	if token == "" {
		key = "access_token"
		token = query.Get(key)
	}
	if token != "" {
		values.Set(key, token)
	}
	if mediaLink := query.Get("media_link"); mediaLink != "" {
		values.Set("media_link", mediaLink)
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func writePlaylist(w io.Writer, reader io.Reader, authSuffix string) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return
	}
	content := string(data)
	if authSuffix == "" {
		io.WriteString(w, content)
		return
	}
	io.WriteString(w, appendPlaylistAuth(content, authSuffix))
}

func appendPlaylistAuth(content, authSuffix string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, `URI="`) {
			lines[i] = appendURIAuth(line, authSuffix)
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "?") {
			continue
		}
		lines[i] = line + authSuffix
	}
	return strings.Join(lines, "\n")
}

func appendURIAuth(line, authSuffix string) string {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return line
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return line
	}
	end += start
	uri := line[start:end]
	if strings.Contains(uri, "?") {
		return line
	}
	return line[:start] + uri + authSuffix + line[end:]
}
