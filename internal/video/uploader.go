package video

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// UploadResult — результат загрузки видео-чанков в IPFS.
type UploadResult struct {
	// MasterCID — CID мастер-плейлиста master.m3u8
	MasterCID string `json:"master_cid"`
	// VariantCIDs — мапа: вариант ("low","medium","high") → CID вариантного плейлиста
	VariantCIDs map[string]string `json:"variant_cids"`
	// SegmentCIDs — мапа: относительный путь чанка → CID
	SegmentCIDs map[string]string `json:"segment_cids"`
	// AllCIDs — все CID (для массового unpin при удалении)
	AllCIDs []string `json:"all_cids"`
}

// Uploader — загружает сгенерированную HLS-структуру в IPFS.
type Uploader struct {
	client *ipfs.Client
}

// NewUploader создаёт новый Uploader.
func NewUploader(client *ipfs.Client) *Uploader {
	return &Uploader{client: client}
}

// UploadDir обходит outputDir и загружает все файлы в IPFS.
// Возвращает структуру с CID мастер-плейлиста, вариантных плейлистов и чанков.
func (u *Uploader) UploadDir(ctx context.Context, outputDir string) (*UploadResult, error) {
	result := &UploadResult{
		VariantCIDs: make(map[string]string),
		SegmentCIDs: make(map[string]string),
	}

	// Первый проход: загружаем все файлы, собираем path → CID
	fileCIDs := make(map[string]string) // относительный путь → CID

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

		addResult, err := u.client.Add(ctx, filepath.Base(path), f)
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

	// Второй проход: переписываем ссылки в плейлистах и перезагружаем
	// Мастер-плейлист ссылается на вариантные плейлисты
	// Вариантные плейлисты ссылаются на чанки
	// Нужно подставить CID вместо путей

	// Сначала обновляем вариантные плейлисты (ссылки на чанки)
	for relPath, cid := range fileCIDs {
		if strings.HasSuffix(relPath, "playlist.m3u8") {
			variantName := filepath.Dir(relPath)
			result.VariantCIDs[variantName] = cid

			// Обновляем плейлист: подставляем CID чанков
			updated, err := u.rewriteVariantPlaylist(outputDir, relPath, fileCIDs)
			if err != nil {
				return nil, fmt.Errorf("rewrite variant playlist %s: %w", relPath, err)
			}

			// Перезагружаем обновлённый плейлист
			newCID, err := u.reuploadContent(ctx, updated, filepath.Base(relPath))
			if err != nil {
				return nil, fmt.Errorf("reupload variant playlist %s: %w", relPath, err)
			}
			result.VariantCIDs[variantName] = newCID
			// Заменяем CID в allCIDs
			result.AllCIDs = replaceInSlice(result.AllCIDs, cid, newCID)
		}
	}

	// Создаём мастер-плейлист с CID-ссылками на вариантные плейлисты
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
		// Строки с именами чанков (seg_0.m4s и т.д.)
		if strings.HasPrefix(trimmed, "seg_") && strings.HasSuffix(trimmed, ".m4s") {
			segRelPath := filepath.Join(variantDir, trimmed)
			if cid, ok := fileCIDs[segRelPath]; ok {
				// Заменяем на CID
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

	// Порядок: low, medium, high
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
		b.WriteString(fmt.Sprintf("/stream/segment/%s\n", cid))
	}

	return b.String(), nil
}

// reuploadContent загружает строку как файл в IPFS
func (u *Uploader) reuploadContent(ctx context.Context, content, filename string) (string, error) {
	reader := io.NopCloser(strings.NewReader(content))
	result, err := u.client.Add(ctx, filename, reader)
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
