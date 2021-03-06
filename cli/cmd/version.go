package cmd

import (
	"context"
	"fmt"
	"os"

	pb "github.com/linkerd/linkerd2/controller/gen/public"
	"github.com/linkerd/linkerd2/pkg/version"
	"github.com/spf13/cobra"
)

const DefaultVersionString = "unavailable"

type versionOptions struct {
	shortVersion      bool
	onlyClientVersion bool
}

func newVersionOptions() *versionOptions {
	return &versionOptions{
		shortVersion:      false,
		onlyClientVersion: false,
	}
}

func newCmdVersion() *cobra.Command {
	options := newVersionOptions()

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the client and server version information",
		Run: func(cmd *cobra.Command, args []string) {
			clientVersion := version.Version
			if options.shortVersion {
				fmt.Println(clientVersion)
			} else {
				fmt.Printf("Client version: %s\n", clientVersion)
			}

			conduitApiClient, err := newPublicAPIClient()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error connecting to server: %s\n", err)
				os.Exit(1)
			}

			if !options.onlyClientVersion {
				serverVersion := getServerVersion(conduitApiClient)
				if options.shortVersion {
					fmt.Println(serverVersion)
				} else {
					fmt.Printf("Server version: %s\n", serverVersion)
				}
			}
		},
	}

	cmd.Args = cobra.NoArgs
	cmd.PersistentFlags().BoolVar(&options.shortVersion, "short", options.shortVersion, "Print the version number(s) only, with no additional output")
	cmd.PersistentFlags().BoolVar(&options.onlyClientVersion, "client", options.onlyClientVersion, "Print the client version only")

	return cmd
}

func getServerVersion(client pb.ApiClient) string {
	resp, err := client.Version(context.Background(), &pb.Empty{})
	if err != nil {
		return DefaultVersionString
	}

	return resp.GetReleaseVersion()
}
