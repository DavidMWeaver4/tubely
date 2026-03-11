package main

import (
	"net/http"
	"fmt"
	"os"
	"io"
	"mime"
	"encoding/base64"
	"crypto/rand"
	"strings"
	"context"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"

	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1 << 30)

	videoIDPath := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDPath)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Failed to Parse uuid", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil{
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT (token, cfg.jwtSecret)
	if err != nil{
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
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
	r.ParseMultipartForm(1 << 30)
	file, header, err := r.FormFile("video")
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

	if mediaType != "video/mp4" {
    	respondWithError(w, http.StatusBadRequest, "Unsupported media type", nil)
    	return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not copy file", err)
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



	tempFile.Seek(0, io.SeekStart)
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(cfg.s3Bucket),
		Key: aws.String(fileName),
		Body: io.Reader(tempFile),
		ContentType: aws.String(mediaType),
	})
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not create bucket", err)
		return
	}
	newVideoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileName)
	videoMeta.VideoURL = &newVideoURL
	err = cfg.db.UpdateVideo(videoMeta)
	if err != nil{
		respondWithError(w, http.StatusBadRequest, "Could not update database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, videoMeta)
}
