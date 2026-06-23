package bundle

const (
	ManifestFilename = "manifest.json"
	OriginalFilename = "original"
)

type Asset struct {
	CID         string `json:"cid,omitempty"`
	Path        string `json:"path"`
	BundlePath  string `json:"bundle_path,omitempty"`
	Format      string `json:"format"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Size        int64  `json:"size"`
}

type Manifest struct {
	CID      string            `json:"cid"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Version  int               `json:"version"`
	Size     int64             `json:"size"`
	Original Asset             `json:"original"`
	Variants map[string]Asset  `json:"variants,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func NewFileManifest(name, contentType, format string, size int64) Manifest {
	return Manifest{
		Name:    name,
		Type:    "file",
		Version: 1,
		Size:    size,
		Original: Asset{
			Path:        "",
			BundlePath:  OriginalFilename,
			Format:      format,
			ContentType: contentType,
			Size:        size,
		},
	}
}

func (m *Manifest) Finalize(rootCID string) {
	m.CID = rootCID
	m.Original.Path = "/file/" + rootCID
	for key, variant := range m.Variants {
		variant.Path = "/file/" + rootCID + "/" + key
		m.Variants[key] = variant
	}
}
