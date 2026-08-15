// audit-dir: Go port of service/scripts/audit_dir.py.
//
// Aggregate AI-provenance audit over a directory tree: recursively inspects
// supported text/image/container files and emits one summary plus a per-file
// finding list with confidence classifications.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"watermarks-remover/internal/cleaning"
	"watermarks-remover/internal/cliutil"
	"watermarks-remover/internal/i18n"
)

func main() {
	var jsonOut bool
	var skip string
	flag.BoolVar(&jsonOut, "json", false, "Emit a JSON report")
	flag.StringVar(&skip, "skip", "", "Comma-separated extra directory names to skip")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	positional := cliutil.ParseAllowInterspersed()
	cliutil.Init(langFlag)
	if len(positional) < 1 {
		flag.Usage()
		os.Exit(2)
	}
	root := positional[0]

	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		cleaning.Eprint(i18n.T("cli.not_a_directory", root))
		os.Exit(2)
	}

	skipDirs := map[string]bool{}
	for k, v := range cleaning.DefaultSkipDirs {
		skipDirs[k] = v
	}
	for _, name := range strings.Split(skip, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			skipDirs[name] = true
		}
	}

	var files []map[string]interface{}
	var skipped []map[string]interface{}
	for _, path := range cleaning.WalkAuditFiles(root, skipDirs) {
		st, err := os.Stat(path)
		if err == nil && st.Size() > int64(cleaning.MaxInputBytes) {
			skipped = append(skipped, map[string]interface{}{"path": path, "reason": "too large"})
			continue
		}
		item := cleaning.ScanFile(path, "")
		if item.Err != "" {
			// keep the audit going on one bad file
			skipped = append(skipped, map[string]interface{}{"path": path, "reason": item.Err})
			continue
		}
		files = append(files, item.ToDict())
	}

	summary := cleaning.Aggregate(files)
	if skipped == nil {
		skipped = []map[string]interface{}{}
	}
	if files == nil {
		files = []map[string]interface{}{}
	}
	report := map[string]interface{}{
		"root":          root,
		"files_scanned": len(files),
		"files_skipped": skipped,
		"summary":       summary,
		"files":         files,
	}

	if jsonOut {
		if err := cleaning.EmitJSON(report); err != nil {
			cleaning.Eprint("error: " + err.Error())
			os.Exit(1)
		}
	} else {
		cleaning.PrintHumanReport(files, summary, [][2]string{
			{"audit.root", report["root"].(string)},
			{"audit.files_skipped", fmt.Sprintf("%d", len(skipped))},
		})
	}

	if n, _ := summary["actionable_files"].(int); n > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}
