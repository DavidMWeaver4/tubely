package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"path/filepath"
	"mime"
	"encoding/base64"
	"crypto/rand"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20

	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Not in correct format", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
    	respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
    	return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
    	respondWithError(w, http.StatusBadRequest, "Unsupported media type", nil)
    	return
	}
	videoMeta, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not fetch video data", err)
		return
	}
	if videoMeta.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "This video does not belong to user", nil)
		return
	}
	randomURL := make([]byte, 32)
	_, err = rand.Read(randomURL)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Failed to create URL", err)
		return
	}

	encodedURL := base64.RawURLEncoding.EncodeToString(randomURL)
	split := strings.Split(mediaType, "/")
	fileName := fmt.Sprintf("%s.%s", encodedURL, split[1])
	filePath := filepath.Join(cfg.assetsRoot, fileName)
	thumbURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, fileName)

	newFile, err := os.Create(filePath)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not create file", err)
		return
	}
	_, err = io.Copy(newFile, file)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not copy file", err)
		return
	}
	videoMeta.ThumbnailURL = &thumbURL

	err = cfg.db.UpdateVideo(videoMeta)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Not not store to database", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMeta)
}
