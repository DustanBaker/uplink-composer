//go:build !windows && !darwin && !linux

package awake

func keep(string) func() { return func() {} }
