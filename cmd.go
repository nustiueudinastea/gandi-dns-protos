package main

import (
	"github.com/spf13/cobra"
)

func main() {
	var apiKey string
	var timerInterval int64

	var rootCmd = &cobra.Command{
		Use:   "gandi-dns-protos",
		Short: "Gandi DNS is a Protos resource provider",
		Long: `Gandi DNS is a resource provider that runs as an application on top of Protos,
and uses the LiveDNS API provided by Gandi.`,
		Run: func(cmd *cobra.Command, args []string) {
			start(apiKey, timerInterval)
		},
	}

	rootCmd.PersistentFlags().StringVarP(&apiKey, "apikey", "k", "", "Gandi LiveDNS API key")
	rootCmd.MarkPersistentFlagRequired("apikey")
	rootCmd.PersistentFlags().Int64VarP(&timerInterval, "interval", "i", 300, "Timer interval for checking all the resources")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
