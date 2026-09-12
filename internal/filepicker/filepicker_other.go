//go:build !windows && !darwin && !linux

package filepicker

import "context"

func pickFolder(_ context.Context, _ string) (string, error) { return "", ErrUnavailable }

func pickFile(_ context.Context, _, _ string, _ []string) (string, error) {
	return "", ErrUnavailable
}
