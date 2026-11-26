package main

import (
	"io"
	"mime/multipart"
)

func multipartTorrent(w io.Writer, name string, data []byte) (string, error) {
	mw := multipart.NewWriter(w)
	fw, err := mw.CreateFormFile("torrents", name)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	return mw.FormDataContentType(), nil
}
