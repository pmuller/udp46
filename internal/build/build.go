package build

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("udp46 version=%s commit=%s date=%s", Version, Commit, Date)
}
