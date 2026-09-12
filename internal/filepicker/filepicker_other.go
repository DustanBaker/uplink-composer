//go:build !windows && !darwin && !linux

package filepicker

import "context"

func pickFolder(_ context.Context, _ string) (string, error) { return "", ErrUnavailable }
func pickImage(_ context.Context, _ string) (string, error)  { return "", ErrUnavailable }
