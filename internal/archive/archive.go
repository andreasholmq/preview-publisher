package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type Stats struct {
	SizeBytes int64
	FileCount int
}

var rootAbsoluteRef = regexp.MustCompile(`(?i)\b(?:src|href|action)\s*=\s*["']/|url\(\s*["']?/`)

func ExtractTarGz(r io.Reader, dest string, maxFiles int) (Stats, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Stats{}, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var stats Stats
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Stats{}, err
		}
		name, err := cleanArchivePath(hdr.Name)
		if err != nil {
			return Stats{}, err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err := ensureWithin(dest, target); err != nil {
			return Stats{}, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return Stats{}, err
			}
		case tar.TypeReg, tar.TypeRegA:
			stats.FileCount++
			if stats.FileCount > maxFiles {
				return Stats{}, fmt.Errorf("archive has more than %d files", maxFiles)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return Stats{}, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return Stats{}, err
			}
			written, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return Stats{}, copyErr
			}
			if closeErr != nil {
				return Stats{}, closeErr
			}
			stats.SizeBytes += written
		default:
			return Stats{}, fmt.Errorf("unsupported archive entry type for %s", hdr.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Stats{}, errors.New("archive must contain root index.html")
		}
		return Stats{}, err
	}
	return stats, nil
}

func CreateTarGz(sourcePath, outputPath string) ([]string, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	var warnings []string
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(sourcePath, "index.html")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("directory must contain root index.html")
			}
			return nil, err
		}
		err = filepath.WalkDir(sourcePath, func(filePath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(sourcePath, filePath)
			if err != nil {
				return err
			}
			archiveName := filepath.ToSlash(rel)
			fileWarnings, err := addFile(tw, filePath, archiveName, info)
			if err != nil {
				return err
			}
			warnings = append(warnings, fileWarnings...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return warnings, nil
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path must be a directory or regular .html file")
	}
	if strings.ToLower(filepath.Ext(sourcePath)) != ".html" {
		return nil, errors.New("single-file publish requires a .html file")
	}
	fileWarnings, err := addFile(tw, sourcePath, "index.html", info)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, fileWarnings...)
	return warnings, nil
}

func addFile(tw *tar.Writer, filePath, archiveName string, info os.FileInfo) ([]string, error) {
	if err := tw.WriteHeader(&tar.Header{
		Name:    archiveName,
		Mode:    0o644,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}); err != nil {
		return nil, err
	}
	in, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	content, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(filePath), ".html") && rootAbsoluteRef.Match(content) {
		return []string{fmt.Sprintf("%s contains root-absolute asset references that may break under a route prefix", filePath)}, nil
	}
	return nil, nil
}

func cleanArchivePath(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return "", errors.New("archive entry path is empty")
	}
	if strings.HasPrefix(name, "/") || path.IsAbs(name) {
		return "", fmt.Errorf("archive entry %q is absolute", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive entry %q escapes extraction directory", name)
	}
	return cleaned, nil
}

func ensureWithin(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("target escapes extraction directory")
	}
	return nil
}
