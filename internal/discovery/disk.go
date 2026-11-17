package discovery

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sort"

	"wool/internal/config"
	"wool/internal/gguf"
)

// Disk status values for a configured model path.
const (
	DiskStatusOK         = "ok"
	DiskStatusMissing    = "missing"
	DiskStatusUnreadable = "unreadable"
)

// DiskOptions controls the read-only disk report. Hash reads every file in
// full and is much slower than the default size-and-metadata fingerprint.
type DiskOptions struct {
	Hash bool
	// SkipContent keeps the report to stat-only facts (size, missing, unused
	// and duplicate paths) when Hash is false. The TUI uses it to stay
	// responsive.
	SkipContent bool
}

// DiskModel is one [models.<id>] entry in a disk report.
type DiskModel struct {
	ID               string   `json:"id"`
	Path             string   `json:"path"`
	Alias            string   `json:"alias,omitempty"`
	Size             uint64   `json:"size_bytes"`
	Status           string   `json:"status"`
	Presets          []string `json:"presets,omitempty"`
	DuplicatePath    bool     `json:"duplicate_path,omitempty"`
	DuplicateContent bool     `json:"duplicate_content,omitempty"`
	Hash             string   `json:"sha256,omitempty"`
}

// PathDuplicate lists model IDs that point at the same file.
type PathDuplicate struct {
	Path   string   `json:"path"`
	Models []string `json:"models"`
}

// ContentDuplicate lists model IDs whose files look identical. Hash is set
// only when the report was generated with full content hashing.
type ContentDuplicate struct {
	Size   uint64   `json:"size_bytes"`
	Hash   string   `json:"sha256,omitempty"`
	Models []string `json:"models"`
}

// DiskReport is a read-only inventory of the configured models.
type DiskReport struct {
	Models             []DiskModel        `json:"models"`
	TotalBytes         uint64             `json:"total_bytes"`
	UniqueFiles        int                `json:"unique_files"`
	Missing            int                `json:"missing"`
	Unused             []string           `json:"unused"`
	DuplicatePaths     []PathDuplicate    `json:"duplicate_paths,omitempty"`
	DuplicateContent   []ContentDuplicate `json:"duplicate_content,omitempty"`
	DuplicateDetection string             `json:"duplicate_detection"`
	Warnings           []Warning          `json:"warnings,omitempty"`
}

// Find returns the report entry for a model ID.
func (r DiskReport) Find(id string) (DiskModel, bool) {
	i := sort.Search(len(r.Models), func(i int) bool { return r.Models[i].ID >= id })
	if i < len(r.Models) && r.Models[i].ID == id {
		return r.Models[i], true
	}
	return DiskModel{}, false
}

type diskFile struct {
	size        uint64
	status      string
	canonical   string
	fingerprint string
	hash        string
}

// Disk reports per-model size, total usage, preset usage, duplicate paths and
// missing files. It never writes to the config or the filesystem.
func Disk(cfg *config.Config, opts DiskOptions) DiskReport {
	rep := DiskReport{
		Models:             []DiskModel{},
		Unused:             []string{},
		DuplicatePaths:     []PathDuplicate{},
		DuplicateContent:   []ContentDuplicate{},
		DuplicateDetection: "size+metadata",
	}
	if opts.Hash {
		rep.DuplicateDetection = "sha256"
	} else if opts.SkipContent {
		rep.DuplicateDetection = "none"
	}
	if cfg == nil {
		return rep
	}

	usedBy := map[string][]string{}
	for _, presetID := range cfg.PresetIDs() {
		modelID := cfg.Presets[presetID].Model
		if modelID == "" {
			continue
		}
		usedBy[modelID] = append(usedBy[modelID], presetID)
	}

	files := map[string]*diskFile{}
	fileFor := func(abs, canonical string) *diskFile {
		key := canonical
		if key == "" {
			key = abs
		}
		if f, ok := files[key]; ok {
			return f
		}
		f := &diskFile{canonical: canonical, status: DiskStatusOK}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			if os.IsNotExist(err) {
				f.status = DiskStatusMissing
			} else {
				f.status = DiskStatusUnreadable
			}
		case info.IsDir():
			f.status = DiskStatusUnreadable
		default:
			f.size = uint64(info.Size())
			rep.UniqueFiles++
			rep.TotalBytes += f.size
			if opts.Hash {
				sum, err := hashFile(abs)
				if err != nil {
					rep.Warnings = append(rep.Warnings, Warning{Path: abs, Message: "hash: " + err.Error()})
				} else {
					f.hash = sum
				}
			} else if !opts.SkipContent {
				fingerprint, err := metadataFingerprint(abs, f.size)
				if err != nil {
					rep.Warnings = append(rep.Warnings, Warning{Path: abs, Message: "metadata: " + err.Error()})
				} else {
					f.fingerprint = fingerprint
				}
			}
		}
		files[key] = f
		return f
	}

	pathGroups := map[string][]string{}
	contentGroups := map[string][]string{}
	contentPaths := map[string]map[string]bool{}
	contentSize := map[string]uint64{}
	contentHash := map[string]string{}

	for _, id := range cfg.ModelIDs() {
		mc := cfg.Models[id]
		abs := config.AbsPath(mc.Path)
		canonical := config.CanonicalPath(mc.Path)
		file := fileFor(abs, canonical)
		dm := DiskModel{
			ID:      id,
			Path:    abs,
			Alias:   mc.Alias,
			Size:    file.size,
			Status:  file.status,
			Presets: usedBy[id],
		}
		if dm.Presets == nil {
			dm.Presets = []string{}
		}
		if len(dm.Presets) == 0 {
			rep.Unused = append(rep.Unused, id)
		}
		if dm.Status != DiskStatusOK {
			rep.Missing++
		}
		if file.status == DiskStatusOK {
			key := ""
			switch {
			case opts.Hash && file.hash != "":
				dm.Hash = file.hash
				key = "sha256:" + file.hash
				contentHash[key] = file.hash
			case !opts.Hash && !opts.SkipContent && file.fingerprint != "":
				key = "meta:" + file.fingerprint
			}
			if key != "" {
				contentGroups[key] = append(contentGroups[key], id)
				contentSize[key] = file.size
				if contentPaths[key] == nil {
					contentPaths[key] = map[string]bool{}
				}
				contentPaths[key][abs] = true
			}
		}
		if canonical != "" {
			pathGroups[canonical] = append(pathGroups[canonical], id)
		}
		rep.Models = append(rep.Models, dm)
	}

	for _, ids := range pathGroups {
		if len(ids) > 1 {
			for i := range rep.Models {
				for _, id := range ids {
					if rep.Models[i].ID == id {
						rep.Models[i].DuplicatePath = true
					}
				}
			}
		}
	}
	for key, ids := range contentGroups {
		if len(ids) > 1 && len(contentPaths[key]) > 1 {
			for i := range rep.Models {
				for _, id := range ids {
					if rep.Models[i].ID == id {
						rep.Models[i].DuplicateContent = true
					}
				}
			}
		}
	}

	keys := make([]string, 0, len(pathGroups))
	for key, ids := range pathGroups {
		if len(ids) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		rep.DuplicatePaths = append(rep.DuplicatePaths, PathDuplicate{Path: key, Models: pathGroups[key]})
	}

	keys = keys[:0]
	for key, ids := range contentGroups {
		if len(ids) > 1 && len(contentPaths[key]) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := ContentDuplicate{Size: contentSize[key], Models: contentGroups[key]}
		if opts.Hash {
			group.Hash = contentHash[key]
		}
		rep.DuplicateContent = append(rep.DuplicateContent, group)
	}

	sort.Slice(rep.Warnings, func(i, j int) bool {
		if rep.Warnings[i].Path != rep.Warnings[j].Path {
			return rep.Warnings[i].Path < rep.Warnings[j].Path
		}
		return rep.Warnings[i].Message < rep.Warnings[j].Message
	})
	if rep.Warnings == nil {
		rep.Warnings = []Warning{}
	}
	return rep
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// metadataFingerprint is the cheap default identity: size plus the GGUF
// general metadata. Two different quantizations almost always differ in size,
// and --hash is available when an exact answer is needed.
func metadataFingerprint(path string, size uint64) (string, error) {
	meta, err := gguf.ReadMeta(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d|%s|%s|%d|%d|%d",
		size, meta.Name, meta.Architecture, meta.Parameters(), meta.CtxTrain, len(meta.Tensors)), nil
}
