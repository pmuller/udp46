package metrics

import (
	"encoding/json"
	"net/http"
)

func DebugSessionsHandler(snapshots SnapshotFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if snapshots == nil {
			_, _ = w.Write([]byte("[]\n"))
			return
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(snapshots())
	})
}
