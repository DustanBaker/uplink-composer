//go:build !windows && !linux

package hwdetect

import (
	"context"
	"fmt"
	"runtime"
)

func detect(_ context.Context) (*Hardware, error) {
	return nil, fmt.Errorf("hardware detection is not supported on %s yet — build on a Windows or Linux machine of the target model", runtime.GOOS)
}
