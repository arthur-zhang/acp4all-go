//go:build !windows

package main

import "os"

func isRootUser() bool {
	return os.Geteuid() == 0
}
