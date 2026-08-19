package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// uploadErrorResponse is the stable public contract for validation failures.
// Code is for clients; Message is already localized for direct presentation.
type uploadErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeUploadError(w http.ResponseWriter, r *http.Request, status int, code string, details map[string]any) {
	writeJSON(w, status, uploadErrorResponse{
		Code:    code,
		Message: localizedUploadMessage(uploadLocale(r), code, details),
		Details: details,
	})
}

func uploadLocale(r *http.Request) string {
	if language := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); strings.HasPrefix(language, "ru") {
		return "ru"
	}
	if language := strings.ToLower(r.Header.Get("Accept-Language")); strings.HasPrefix(language, "ru") {
		return "ru"
	}
	return "en"
}

func localizedUploadMessage(locale, code string, details map[string]any) string {
	maxSize := humanFileSize(numberDetail(details, "max_bytes"), locale)
	maxDuration := int(numberDetail(details, "max_duration_sec"))
	expectedRatio, _ := details["expected_aspect_ratio"].(string)
	if locale == "ru" {
		switch code {
		case "upload_missing_file":
			return "Выберите файл для загрузки."
		case "unsupported_file_type", "unsupported_video_format":
			return "Этот формат файла не поддерживается."
		case "file_too_large", "video_file_too_large":
			return fmt.Sprintf("Размер файла превышает допустимые %s.", maxSize)
		case "video_duration_exceeded":
			return fmt.Sprintf("Длительность видео превышает допустимые %d сек.", maxDuration)
		case "video_aspect_ratio_invalid":
			return fmt.Sprintf("Можно загрузить только вертикальное видео %s.", expectedRatio)
		case "video_metadata_invalid":
			return "Не удалось определить параметры видео. Выберите корректный видеофайл."
		case "upload_form_invalid":
			return "Не удалось обработать форму загрузки. Повторите попытку."
		case "upload_storage_unavailable":
			return "Хранилище временно недоступно. Повторите попытку позже."
		default:
			return "Не удалось загрузить файл. Повторите попытку."
		}
	}

	switch code {
	case "upload_missing_file":
		return "Choose a file to upload."
	case "unsupported_file_type", "unsupported_video_format":
		return "This file format is not supported."
	case "file_too_large", "video_file_too_large":
		return fmt.Sprintf("The file exceeds the %s limit.", maxSize)
	case "video_duration_exceeded":
		return fmt.Sprintf("The video exceeds the %d second duration limit.", maxDuration)
	case "video_aspect_ratio_invalid":
		return fmt.Sprintf("Only vertical %s video can be uploaded.", expectedRatio)
	case "video_metadata_invalid":
		return "Video details could not be read. Choose a valid video file."
	case "upload_form_invalid":
		return "The upload form could not be processed. Try again."
	case "upload_storage_unavailable":
		return "Storage is temporarily unavailable. Try again later."
	default:
		return "The file could not be uploaded. Try again."
	}
}

func numberDetail(details map[string]any, key string) int64 {
	if details == nil {
		return 0
	}
	switch value := details[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		return 0
	}
}

func humanFileSize(bytes int64, locale string) string {
	if bytes <= 0 {
		if locale == "ru" {
			return "установленный"
		}
		return "configured"
	}
	const megabyte = 1024 * 1024
	if bytes%megabyte == 0 {
		unit := "MB"
		if locale == "ru" {
			unit = "МБ"
		}
		return fmt.Sprintf("%d %s", bytes/megabyte, unit)
	}
	unit := "KB"
	if locale == "ru" {
		unit = "КБ"
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/1024, unit)
}
