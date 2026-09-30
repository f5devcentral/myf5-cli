package downloads

import (
	"encoding/json"
	"strconv"
)

// HashItem represents a cryptographic hash for a download file.
type HashItem struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// FileItem represents an individual downloadable artifact (e.g., ISO, OVA, RPM).
type FileItem struct {
	Name        string     `json:"name"`
	FileSize    int64      `json:"fileSize"`
	Description string     `json:"description,omitempty"`
	Hashes      []HashItem `json:"hashes,omitempty"`
}

func (f *FileItem) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name        string          `json:"name"`
		Filename    string          `json:"filename"`
		FileSize    int64           `json:"fileSize"`
		Bytes       json.RawMessage `json:"bytes"`
		Description string          `json:"description"`
		Hashes      []HashItem      `json:"hashes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	f.Name = raw.Name
	if f.Name == "" {
		f.Name = raw.Filename
	}
	f.Description = raw.Description
	f.Hashes = raw.Hashes
	f.FileSize = raw.FileSize

	if f.FileSize == 0 && len(raw.Bytes) > 0 {
		var num int64
		if err := json.Unmarshal(raw.Bytes, &num); err == nil {
			f.FileSize = num
		} else {
			var str string
			if err := json.Unmarshal(raw.Bytes, &str); err == nil {
				if parsed, pErr := strconv.ParseInt(str, 10, 64); pErr == nil {
					f.FileSize = parsed
				}
			}
		}
	}

	return nil
}

// Container represents a category/grouping of files (e.g. "Virtual Edition", "Base ISO").
type Container struct {
	Name  string     `json:"name"`
	Files []FileItem `json:"files,omitempty"`
}

func (c *Container) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string     `json:"name"`
		Container string     `json:"container"`
		Files     []FileItem `json:"files"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Name = raw.Name
	if c.Name == "" {
		c.Name = raw.Container
	}
	c.Files = raw.Files
	return nil
}

// ProductVersion represents a software release version.
type ProductVersion struct {
	Name        string      `json:"name"`
	ReleaseDate string      `json:"releaseDate,omitempty"`
	Containers  []Container `json:"containers,omitempty"`
}

func (pv *ProductVersion) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name        string      `json:"name"`
		Version     string      `json:"version"`
		ReleaseDate string      `json:"releaseDate"`
		Containers  []Container `json:"containers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	pv.Name = raw.Name
	if pv.Name == "" {
		pv.Name = raw.Version
	}
	pv.ReleaseDate = raw.ReleaseDate
	pv.Containers = raw.Containers
	return nil
}

// ProductLine represents a major/minor branch (e.g. big-ip_v17.x).
type ProductLine struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

// ProductFamily represents a product family (e.g. BIG-IP, BIG-IQ, NGINX).
type ProductFamily struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"displayName,omitempty"`
	ProductLines []ProductLine `json:"productLines,omitempty"`
}

// DownloadsMetadataResponse is the API response for /downloads-management/v1/downloads/metadata.
type DownloadsMetadataResponse struct {
	Data struct {
		ProductFamilies []ProductFamily `json:"productFamilies"`
	} `json:"data"`
}

// ProductVersionsResponse is the API response for /downloads-management/v1/downloads/product/{family}/{line}.
type ProductVersionsResponse struct {
	Family   string           `json:"family"`
	Product  string           `json:"product"`
	Versions []ProductVersion `json:"versions"`
	Data     struct {
		ProductVersions []ProductVersion `json:"productVersions"`
	} `json:"data"`
}

// GetVersions returns versions from either root 'versions' or wrapped 'data.productVersions'.
func (p *ProductVersionsResponse) GetVersions() []ProductVersion {
	if len(p.Versions) > 0 {
		return p.Versions
	}
	return p.Data.ProductVersions
}

// DownloadLink represents a signed regional CDN mirror URL.
type DownloadLink struct {
	Href      string `json:"href"`
	Region    string `json:"region,omitempty"`
	Location  string `json:"location,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// GetRegion returns Region or Location.
func (d *DownloadLink) GetRegion() string {
	if d.Region != "" {
		return d.Region
	}
	return d.Location
}

// FileLinksResponse is the API response for /downloads-management/v1/downloads/product/.../{file}.
type FileLinksResponse struct {
	DownloadLinks []DownloadLink `json:"downloadLinks"`
	Filename      string         `json:"filename"`
	Bytes         string         `json:"bytes"`
	MD5           string         `json:"md5"`
	Meta          map[string]any `json:"meta"`
	Data          struct {
		DownloadLinks []DownloadLink `json:"downloadLinks"`
		Hashes        []HashItem     `json:"hashes,omitempty"`
	} `json:"data"`
}

// GetLinks returns download links from root or data wrapper.
func (r *FileLinksResponse) GetLinks() []DownloadLink {
	if len(r.DownloadLinks) > 0 {
		return r.DownloadLinks
	}
	return r.Data.DownloadLinks
}

// GetAllHashes extracts all hashes from root md5/meta and data.hashes.
func (r *FileLinksResponse) GetAllHashes() []HashItem {
	var items []HashItem
	if len(r.Data.Hashes) > 0 {
		items = append(items, r.Data.Hashes...)
	}
	if r.MD5 != "" {
		items = append(items, HashItem{Algorithm: "md5", Value: r.MD5})
	}
	if r.Meta != nil {
		for k, v := range r.Meta {
			if str, ok := v.(string); ok && str != "" {
				items = append(items, HashItem{Algorithm: k, Value: str})
			}
		}
	}
	return items
}
