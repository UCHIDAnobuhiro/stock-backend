package logodetection

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyImage は画像データが空の場合のエラーです。
	ErrEmptyImage = errors.New("image data is empty")

	// ErrImageTooLarge は画像データが MaxImageSize を超える場合のエラーです。
	ErrImageTooLarge = fmt.Errorf("image size exceeds maximum of %d bytes", MaxImageSize)

	// ErrEmptyCompanyName は企業名が空の場合のエラーです。
	ErrEmptyCompanyName = errors.New("company name is required")

	// ErrCompanyNameTooLong は企業名が MaxCompanyNameLength を超える場合のエラーです。
	ErrCompanyNameTooLong = fmt.Errorf("company name exceeds maximum length of %d characters", MaxCompanyNameLength)

	// ErrInvalidCompanyName は企業名に許可されていない文字が含まれる場合のエラーです。
	ErrInvalidCompanyName = errors.New("company name contains invalid characters")
)
