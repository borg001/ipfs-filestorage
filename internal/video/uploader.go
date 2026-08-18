package video

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// IPFSAdder — интерфейс для добавления файлов в IPFS.
// В продакшене реализуется *ipfs.Client, в тестах — mock.
type IPFSAdder interface {
	Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error)
}

// UploadResult — результат загрузки видео-чанков в IPFS.
type UploadResult struct {
	MasterCID         string                       `json:"master_cid"`
	VariantCIDs       map[string]string            `json:"variant_cids"`
	SegmentCIDs       map[string]string            `json:"segment_cids"`
	PosterCIDs        map[string]string            `json:"poster_cids"`
	PrivacyPosterCIDs map[string]map[string]string `json:"privacy_poster_cids"`
	AllCIDs           []string                     `json:"all_cids"`
}

// Uploader загружает сгенерированную HLS-структуру в IPFS.
type Uploader struct {
	adder IPFSAdder
}

// NewUploader создаёт новый Uploader.
func NewUploader(adder IPFSAdder) *Uploader {
	return &Uploader{adder: adder}
}

// UploadDir обходит outputDir и загружает все файлы в IPFS.
func (u *Uploader) UploadDir(ctx context.Context, outputDir string) (*UploadResult, error) {
	result := &UploadResult{
		VariantCIDs:       make(map[string]string),
		SegmentCIDs:       make(map[string]string),
		PosterCIDs:        make(map[string]string),
		PrivacyPosterCIDs: make(map[string]map[string]string),
	}

	if u.adder == nil {
		return nil, fmt.Errorf("ipfs adder not configured")
	}

	// Первый проход: загружаем все файлы, собираем path → CID
	fileCIDs := make(map[string]string)

	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", path, err)
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()

		addResult, err := u.adder.Add(ctx, filepath.Base(path), f)
		if err != nil {
			return fmt.Errorf("ipfs add %s: %w", relPath, err)
		}

		fileCIDs[relPath] = addResult.CID
		result.AllCIDs = append(result.AllCIDs, addResult.CID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk output dir: %w", err)
	}

	if len(fileCIDs) == 0 {
		return nil, fmt.Errorf("no files found in output directory")
	}

	// Обновляем вариантные плейлисты (ссылки на чанки)
	for relPath, cid := range fileCIDs {
		if strings.HasSuffix(relPath, "playlist.m3u8") {
			variantName := filepath.Dir(relPath)
			result.VariantCIDs[variantName] = cid

			updated, err := u.rewriteVariantPlaylist(outputDir, relPath, fileCIDs)
			if err != nil {
				return nil, fmt.Errorf("rewrite variant playlist %s: %w", relPath, err)
			}

			newCID, err := u.reuploadContent(ctx, updated, filepath.Base(relPath))
			if err != nil {
				return nil, fmt.Errorf("reupload variant playlist %s: %w", relPath, err)
			}
			result.VariantCIDs[variantName] = newCID
			result.AllCIDs = replaceInSlice(result.AllCIDs, cid, newCID)
		}
	}

	// Создаём мастер-плейлист
	masterContent, err := u.buildMasterPlaylist(result.VariantCIDs, fileCIDs)
	if err != nil {
		return nil, fmt.Errorf("build master playlist: %w", err)
	}
	masterCID, err := u.reuploadContent(ctx, masterContent, "master.m3u8")
	if err != nil {
		return nil, fmt.Errorf("upload master playlist: %w", err)
	}
	result.MasterCID = masterCID
	result.AllCIDs = append(result.AllCIDs, masterCID)

	// Заполняем SegmentCIDs
	for relPath, cid := range fileCIDs {
		if strings.HasSuffix(relPath, ".m4s") {
			result.SegmentCIDs[relPath] = cid
		}
		if poster, ok := posterFromPath(relPath); ok {
			if poster.variant == "" {
				result.PosterCIDs[poster.size] = cid
				continue
			}
			if result.PrivacyPosterCIDs[poster.variant] == nil {
				result.PrivacyPosterCIDs[poster.variant] = make(map[string]string)
			}
			result.PrivacyPosterCIDs[poster.variant][poster.size] = cid
		}
	}

	return result, nil
}

// rewriteVariantPlaylist читает вариантный плейлист и подставляет CID чанков вместо путей
func (u *Uploader) rewriteVariantPlaylist(outputDir, relPath string, fileCIDs map[string]string) (string, error) {
	fullPath := filepath.Join(outputDir, relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	var result strings.Builder
	variantDir := filepath.Dir(relPath)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
			initRelPath := filepath.Join(variantDir, "init.mp4")
			if cid, ok := fileCIDs[initRelPath]; ok {
				result.WriteString(strings.Replace(line, `URI="init.mp4"`, fmt.Sprintf(`URI="%s.mp4"`, cid), 1) + "\n")
				continue
			}
		}
		if strings.HasPrefix(trimmed, "seg_") && strings.HasSuffix(trimmed, ".m4s") {
			segRelPath := filepath.Join(variantDir, trimmed)
			if cid, ok := fileCIDs[segRelPath]; ok {
				result.WriteString(cid + ".m4s\n")
				continue
			}
		}
		result.WriteString(line + "\n")
	}

	return result.String(), nil
}

// buildMasterPlaylist генерирует мастер-плейлист с CID-ссылками
func (u *Uploader) buildMasterPlaylist(variantCIDs map[string]string, fileCIDs map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")

	for _, poster := range collectPosters(fileCIDs) {
		if poster.variant == "" {
			b.WriteString(fmt.Sprintf("#EXT-X-IAMFREE-POSTER:SIZE=%s,URI=\"../segment/%s.jpg\"\n", poster.size, poster.cid))
			continue
		}
		b.WriteString(fmt.Sprintf("#EXT-X-IAMFREE-POSTER:VARIANT=%s,SIZE=%s,URI=\"../segment/%s.jpg\"\n", poster.variant, poster.size, poster.cid))
	}

	order := []string{"low", "medium", "high"}
	bandwidth := map[string]int{
		"low":    500000,
		"medium": 1500000,
		"high":   4000000,
	}
	resolution := map[string]string{
		"low":    "426x760",
		"medium": "720x1280",
		"high":   "1080x1920",
	}

	for _, name := range order {
		cid, ok := variantCIDs[name]
		if !ok {
			continue
		}
		bw := bandwidth[name]
		res := resolution[name]
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s,CODECS=\"avc1.42e01e\"\n", bw, res))
		b.WriteString(fmt.Sprintf("../segment/%s.m3u8\n", cid))
	}

	return b.String(), nil
}

type posterEntry struct {
	variant string
	size    string
	cid     string
}

func collectPosters(fileCIDs map[string]string) []posterEntry {
	if len(fileCIDs) == 0 {
		return nil
	}
	posters := make([]posterEntry, 0)
	for relPath, cid := range fileCIDs {
		poster, ok := posterFromPath(relPath)
		if !ok {
			continue
		}
		posters = append(posters, posterEntry{variant: poster.variant, size: poster.size, cid: cid})
	}
	sort.Slice(posters, func(i, j int) bool {
		if posters[i].variant != posters[j].variant {
			return posters[i].variant < posters[j].variant
		}
		return posterArea(posters[i].size) < posterArea(posters[j].size)
	})
	return posters
}

type posterPath struct {
	variant string
	size    string
}

func posterSizeFromPath(relPath string) (string, bool) {
	poster, ok := posterFromPath(relPath)
	return poster.size, ok
}

func posterFromPath(relPath string) (posterPath, bool) {
	relPath = filepath.ToSlash(relPath)
	if !strings.HasPrefix(relPath, "posters/") || !strings.HasSuffix(strings.ToLower(relPath), ".jpg") {
		return posterPath{}, false
	}
	path := strings.TrimPrefix(relPath, "posters/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 && len(parts) != 2 {
		return posterPath{}, false
	}
	size := strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(path))
	if size == "" {
		return posterPath{}, false
	}
	variant := ""
	if len(parts) == 2 {
		variant = parts[0]
	}
	return posterPath{variant: variant, size: size}, true
}

func posterArea(size string) int {
	var width, height int
	if _, err := fmt.Sscanf(size, "%dx%d", &width, &height); err != nil {
		return 0
	}
	return width * height
}

// reuploadContent загружает строку как файл в IPFS
func (u *Uploader) reuploadContent(ctx context.Context, content, filename string) (string, error) {
	reader := io.NopCloser(strings.NewReader(content))
	result, err := u.adder.Add(ctx, filename, reader)
	if err != nil {
		return "", err
	}
	return result.CID, nil
}

// replaceInSlice заменяет элемент в слайсе
func replaceInSlice(s []string, old, new string) []string {
	for i, v := range s {
		if v == old {
			s[i] = new
			break
		}
	}
	return s
}
