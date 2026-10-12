// Command orchestrator moves the workflow tickets of one Multica workspace
// through their workflows, one pass at a time.
//
//	orchestrator run --workspace-id <id> --workflows <dir> [--once] [--interval 20s]
//	orchestrator validate <file>...
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/example-org/workspace/orchestrator/internal/engine"
	"github.com/example-org/workspace/orchestrator/internal/tracker"
	"github.com/example-org/workspace/orchestrator/internal/workflow"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "validate":
		err = validate(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: orchestrator run --workspace-id <id> --workflows <dir> [--once] [--interval 20s]\n       orchestrator validate <file>...")
	os.Exit(2)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	workspaceID := fs.String("workspace-id", os.Getenv("MULTICA_WORKSPACE_ID"), "the Multica workspace")
	dir := fs.String("workflows", ".multica/workflows", "the directory with the workflow files")
	once := fs.Bool("once", false, "run one pass, then stop")
	interval := fs.Duration("interval", 20*time.Second, "the time between passes")
	_ = fs.Parse(args)
	if *workspaceID == "" {
		return fmt.Errorf("set --workspace-id or MULTICA_WORKSPACE_ID")
	}

	e := &engine.Engine{Board: tracker.Multica{WorkspaceID: *workspaceID}, Log: log.Printf}
	for {
		// Read the workflows on every pass, so a change to them needs no
		// restart. An invalid file stops this pass, not the process.
		defs, err := workflow.LoadDir(*dir)
		if err != nil {
			log.Printf("workflows not loaded, skipping this pass: %v", err)
		} else {
			e.Workflows = defs
			if err := e.Pass(); err != nil {
				log.Printf("pass failed: %v", err)
			}
		}
		if *once {
			return nil
		}
		time.Sleep(*interval)
	}
}

func validate(files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("name at least one workflow file")
	}
	failed := false
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err == nil {
			_, err = workflow.Parse(data)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			failed = true
			continue
		}
		fmt.Printf("%s: valid\n", f)
	}
	if failed {
		return fmt.Errorf("some workflows are not valid")
	}
	return nil
}
