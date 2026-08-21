package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"renart/internal/sqllsp"
	"renart/internal/web/service"
)

func SQLLSP() *cli.Command {
	return &cli.Command{
		Name:  "sql-lsp",
		Usage: "start the Renart SQL language server over stdio",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "workspace",
				Usage: "workspace root to index for SQL completions and definitions",
			},
			&cli.BoolFlag{
				Name:  "enable-filesystem-access",
				Value: true,
				Usage: "allow DuckDB SQL analysis to inspect local file-backed tables",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			workspace := c.String("workspace")
			if workspace == "" {
				var err error
				workspace, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			graph, graphErr := service.LoadSQLLSPGraph(ctx, workspace)
			if graphErr != nil {
				fmt.Fprintf(os.Stderr, "renart sql-lsp: workspace graph unavailable, continuing with syntax analysis: %v\n", graphErr)
				graph = sqllsp.CanonicalGraph{Version: 1, WorkspaceURI: sqllsp.FileURI(workspace)}
			} else {
				fmt.Fprintf(os.Stderr, "renart sql-lsp: indexed %d assets and %d relations from %s\n", len(graph.Assets), len(graph.Relations), workspace)
			}
			server := sqllsp.NewWorkspaceServerWithLoader(workspace, graph, service.LoadSQLLSPGraph)
			server.SetDuckDBFilesystemAccess(c.Bool("enable-filesystem-access"))
			return server.Serve(ctx, os.Stdin, os.Stdout)
		},
	}
}
