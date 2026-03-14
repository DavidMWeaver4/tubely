package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"

	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

	videoIDPath := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to Parse uuid", err)
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
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not copy file", err)
		return
	}
	randomURL := make([]byte, 32)
	_, err = rand.Read(randomURL)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to create URL", err)
		return
	}
	encodedURL := base64.RawURLEncoding.EncodeToString(randomURL)
	split := strings.Split(mediaType, "/")
	fileName := fmt.Sprintf("%s.%s", encodedURL, split[1])

	aspectratioString, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to fetch aspect ratio", err)
		return
	}
	aspectRatioType := ""
	if aspectratioString == "16:9" {
		aspectRatioType = "landscape"
	} else if aspectratioString == "9:16" {
		aspectRatioType = "portrait"
	} else {
		aspectRatioType = "other"
	}
	tempFileKey := aspectRatioType + "/" + fileName
	processedVideo, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to process video for fast start", err)
		return
	}

	processedFile, err := os.Open(processedVideo)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not open processed file", err)
		return
	}
	defer processedFile.Close()
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(tempFileKey),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not create bucket", err)
		return
	}
	newVideoURL := fmt.Sprintf("%s/%s", cfg.s3CfDistribution, tempFileKey)
	//bucketAndKey:= fmt.Sprintf("%s,%s", cfg.s3Bucket, tempFileKey)
	videoMeta.VideoURL = &newVideoURL
	err = cfg.db.UpdateVideo(videoMeta)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not update database", err)
		return
	}
	/* 	presignedURL, err := cfg.dbVideoToSignedVideo(videoMeta)
	   	if err != nil{
	   		respondWithError(w, http.StatusBadRequest, "Could not get presignedURL", err)
	   		return
	   	}
	   	videoMeta = presignedURL */
	respondWithJSON(w, http.StatusOK, videoMeta)
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var b bytes.Buffer
	cmd.Stdout = &b
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	type Stream struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}

	type Output struct {
		Streams []Stream `json:"streams"`
	}
	var params Output
	err = json.Unmarshal(b.Bytes(), &params)
	if err != nil {
		return "", err
	}
	if len(params.Streams) == 0 {
		return "", fmt.Errorf("no streams found")
	}
	ratio := float64(params.Streams[0].Width) / float64(params.Streams[0].Height)
	if math.Abs(ratio-16.0/9.0) < 0.01 {
		return "16:9", nil
	} else if math.Abs(ratio-9.0/16.0) < 0.01 {
		return "9:16", nil
	}
	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return outputFilePath, nil
}

/*
func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration)(string, error){
	s3PresignClient := s3.NewPresignClient(s3Client)

	newPSC, err := s3PresignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket:	aws.String(bucket),
		Key:	aws.String(key),
	},
	s3.WithPresignExpires(expireTime))
	if err != nil{
		return "", err
	}
	return newPSC.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video)(database.Video, error){
	if video.VideoURL == nil{
		return video, nil
	}
	splitVU:= strings.Split(*video.VideoURL, ",")
	if len(splitVU) < 2{
		return video, fmt.Errorf("only 1 argument found in VideoURL")
	}
	presigned, err := generatePresignedURL(cfg.s3Client, splitVU[0], splitVU[1], (5 * time.Minute))
	if err != nil{
		return video, err
	}
	video.VideoURL = &presigned
	return video, nil
}
*/
