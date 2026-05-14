package video

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// IPFSAdder — интерфейс для добавления файлов в IPFS.
// В продакшене реализуется *ipfs.Client, в тестах — mock.
type IPFSAdder interface {
	Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error)
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
		VariantCIDs: make(map[string]string),
		SegmentCIDs: make(map[string]string),
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

	// Обновляем вариантные плейлисты
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
	}

	return result, nil
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
