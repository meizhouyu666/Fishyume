package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"
)

var fixedTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	format := flag.String("format", "", "zip or targz")
	input := flag.String("input", "", "engine binary")
	checksum := flag.String("checksum", "", "checksum file")
	output := flag.String("output", "", "archive path")
	flag.Parse()
	files := []string{*input, *checksum}
	var err error
	if *format == "zip" {
		err = writeZip(*output, files)
	} else {
		err = writeTarGz(*output, files)
	}
	if err != nil {
		panic(err)
	}
}

func writeZip(output string, files []string) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	for _, path := range files {
		data, err := os.Open(path)
		if err != nil {
			return err
		}
		header := &zip.FileHeader{Name: filepath.Base(path), Method: zip.Deflate, Modified: fixedTime}
		header.SetMode(0o755)
		entry, err := w.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(entry, data)
		}
		_ = data.Close()
		if err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeTarGz(output string, files []string) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime = fixedTime
	tw := tar.NewWriter(gz)
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: filepath.Base(path), Mode: 0o755, Size: info.Size(), ModTime: fixedTime, AccessTime: fixedTime, ChangeTime: fixedTime}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		data, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, data)
		_ = data.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}
