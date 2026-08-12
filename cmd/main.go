package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "sklein-convarchive",
	Short:   "Archive multi-source conversations into Object Storage",
	Long:    `sklein-convarchive archives conversations from multiple sources into Object Storage using the open JSONL format.`,
	Version: version,
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().String("s3-endpoint", "http://localhost:9000", "S3-compatible endpoint")
	rootCmd.PersistentFlags().String("s3-access-key", "", "S3 access key")
	rootCmd.PersistentFlags().String("s3-secret-key", "", "S3 secret key")
	rootCmd.PersistentFlags().String("s3-bucket", "conversations", "S3 bucket name")
	rootCmd.PersistentFlags().Bool("s3-use-ssl", false, "Use SSL for the S3 endpoint")
	rootCmd.PersistentFlags().Bool("dry-run", false, "Print actions without executing them")
	rootCmd.PersistentFlags().String("timezone", "Europe/Paris", "Timezone used for timestamps in the render (IANA name)")
	rootCmd.PersistentFlags().Bool("encrypt", false, "Encrypt objects with age before uploading them")
	rootCmd.PersistentFlags().String("age-recipient", "", "Age X25519 recipient public key (age1...) used to encrypt objects")
	rootCmd.PersistentFlags().Bool("no-compress", false, "Disable zstd compression (enabled by default)")

	viper.BindPFlag("s3.endpoint", rootCmd.PersistentFlags().Lookup("s3-endpoint"))
	viper.BindPFlag("s3.access_key", rootCmd.PersistentFlags().Lookup("s3-access-key"))
	viper.BindPFlag("s3.secret_key", rootCmd.PersistentFlags().Lookup("s3-secret-key"))
	viper.BindPFlag("s3.bucket", rootCmd.PersistentFlags().Lookup("s3-bucket"))
	viper.BindPFlag("s3.use_ssl", rootCmd.PersistentFlags().Lookup("s3-use-ssl"))
	viper.BindPFlag("dry-run", rootCmd.PersistentFlags().Lookup("dry-run"))
	viper.BindPFlag("timezone", rootCmd.PersistentFlags().Lookup("timezone"))
	viper.BindPFlag("age.encrypt", rootCmd.PersistentFlags().Lookup("encrypt"))
	viper.BindPFlag("age.recipient", rootCmd.PersistentFlags().Lookup("age-recipient"))
	viper.BindPFlag("no_compress", rootCmd.PersistentFlags().Lookup("no-compress"))

	viper.BindEnv("s3.endpoint", "S3_ENDPOINT")
	viper.BindEnv("s3.access_key", "S3_ACCESS_KEY")
	viper.BindEnv("s3.secret_key", "S3_SECRET_KEY")
	viper.BindEnv("s3.bucket", "S3_BUCKET")
	viper.BindEnv("s3.use_ssl", "S3_USE_SSL")
	viper.BindEnv("timezone", "SKLEIN_CONVARCHIVE_TIMEZONE")
	viper.BindEnv("age.encrypt", "SKLEIN_CONVARCHIVE_ENCRYPT")
	viper.BindEnv("age.recipient", "AGE_RECIPIENT")
	viper.BindEnv("no_compress", "SKLEIN_CONVARCHIVE_NO_COMPRESS")
}

func initConfig() {
	viper.SetDefault("s3.endpoint", "http://localhost:9000")
	viper.SetDefault("s3.bucket", "conversations")
	viper.SetDefault("no_compress", false)

	homeDir, err := os.UserHomeDir()
	if err == nil {
		globalConfigDir := filepath.Join(homeDir, ".config", "sklein-convarchive")
		viper.AddConfigPath(globalConfigDir)
		viper.SetConfigName("config")
		viper.SetConfigType("toml")
		if err := viper.ReadInConfig(); err != nil && !isConfigNotFoundError(err) {
			printError("Failed to read global config file: %v", err)
			os.Exit(1)
		}
	}

	viper.AddConfigPath(".")
	viper.SetConfigName(".sklein-convarchive")
	viper.SetConfigType("toml")
	if err := viper.MergeInConfig(); err != nil && !isConfigNotFoundError(err) {
		printError("Failed to read local config file: %v", err)
		os.Exit(1)
	}
}

func isConfigNotFoundError(err error) bool {
	_, ok := err.(viper.ConfigFileNotFoundError)
	return ok
}

func getMattermostAuthConfig() mattermostAuthConfig {
	return mattermostAuthConfig{
		ServerURL: viper.GetString("mattermost.server_url"),
		Token:     viper.GetString("mattermost.token"),
		Username:  viper.GetString("mattermost.username"),
		Password:  viper.GetString("mattermost.password"),
		MFAToken:  viper.GetString("mattermost.mfa_token"),
	}
}

func getS3Config() s3Config {
	return s3Config{
		Endpoint:  viper.GetString("s3.endpoint"),
		AccessKey: viper.GetString("s3.access_key"),
		SecretKey: viper.GetString("s3.secret_key"),
		Bucket:    viper.GetString("s3.bucket"),
		UseSSL:    viper.GetBool("s3.use_ssl"),
	}
}

func getTimezone() *time.Location {
	name := viper.GetString("timezone")
	if name == "" {
		name = "Europe/Paris"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		printError("invalid timezone %q (falling back to UTC): %v", name, err)
		return time.UTC
	}
	return loc
}

func printError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}
