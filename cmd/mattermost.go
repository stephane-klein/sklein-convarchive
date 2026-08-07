package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
	"github.com/stephane-klein/sklein-convarchive/pkg/mattermost"
)

type mattermostAuthConfig struct {
	ServerURL string
	Token     string
	Username  string
	Password  string
	MFAToken  string
}

type s3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

const (
	perPage = 200
)

func init() {
	rootCmd.AddCommand(mattermostCmd)
	mattermostCmd.AddCommand(archiveCmd)
	mattermostCmd.AddCommand(testCmd)
	mattermostCmd.AddCommand(listConversationsCmd)

	mattermostCmd.PersistentFlags().String("server-url", "", "Mattermost server URL")
	mattermostCmd.PersistentFlags().String("token", "", "Mattermost personal access token")
	mattermostCmd.PersistentFlags().String("username", "", "Mattermost username (login auth)")
	mattermostCmd.PersistentFlags().String("password", "", "Mattermost password (login auth)")
	mattermostCmd.PersistentFlags().String("mfa-token", "", "Mattermost MFA token (login auth)")

	viper.BindPFlag("mattermost.server_url", mattermostCmd.PersistentFlags().Lookup("server-url"))
	viper.BindPFlag("mattermost.token", mattermostCmd.PersistentFlags().Lookup("token"))
	viper.BindPFlag("mattermost.username", mattermostCmd.PersistentFlags().Lookup("username"))
	viper.BindPFlag("mattermost.password", mattermostCmd.PersistentFlags().Lookup("password"))
	viper.BindPFlag("mattermost.mfa_token", mattermostCmd.PersistentFlags().Lookup("mfa-token"))

	viper.BindEnv("mattermost.server_url", "MM_SERVER_URL")
	viper.BindEnv("mattermost.token", "MM_TOKEN")
	viper.BindEnv("mattermost.username", "MM_USERNAME")
	viper.BindEnv("mattermost.password", "MM_PASSWORD")
	viper.BindEnv("mattermost.mfa_token", "MM_MFA_TOKEN")
}

var mattermostCmd = &cobra.Command{
	Use:   "mattermost",
	Short: "Mattermost source connector",
	Long:  `Commands for archiving Mattermost conversations.`,
}

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive all Mattermost conversations to Object Storage",
	Long:  `Archives posts from all teams and channels the authenticated user can access, normalized to the common JSONL schema, into Object Storage.`,
	Run: func(cmd *cobra.Command, args []string) {
		runArchive()
	},
}

func init() {
	archiveCmd.Flags().String("conversation", "", "Conversation to archive (channel ID, direct message ID, or name with --team)")
	archiveCmd.Flags().String("team", "", "Team to archive, or the team of the --conversation name")
	archiveCmd.Flags().String("period", "", "Period to archive: YYYY-MM-DD (day), YYYY-MM (month), or YYYY (year)")

	// Hidden alias kept for backwards compatibility.
	archiveCmd.Flags().String("channel", "", "Deprecated alias of --conversation")
	archiveCmd.Flags().MarkHidden("channel")

	viper.BindPFlag("archive.conversation", archiveCmd.Flags().Lookup("conversation"))
	viper.BindPFlag("archive.channel", archiveCmd.Flags().Lookup("channel"))
	viper.BindPFlag("archive.team", archiveCmd.Flags().Lookup("team"))
	viper.BindPFlag("archive.period", archiveCmd.Flags().Lookup("period"))

	viper.BindEnv("archive.conversation", "SKLEIN_CONVARCHIVE_CONVERSATION")
	viper.BindEnv("archive.channel", "SKLEIN_CONVARCHIVE_CHANNEL")
	viper.BindEnv("archive.team", "SKLEIN_CONVARCHIVE_TEAM")
	viper.BindEnv("archive.period", "SKLEIN_CONVARCHIVE_PERIOD")
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test access to the Mattermost server",
	Long:  `Authenticates against the Mattermost server and displays the current user and the number of accessible teams.`,
	Run: func(cmd *cobra.Command, args []string) {
		runTest()
	},
}

var listConversationsCmd = &cobra.Command{
	Use:   "list-conversations",
	Short: "List all conversations accessible for archiving",
	Long:  `Lists all conversations (public channels, private channels, direct messages, group messages) the authenticated user can archive.`,
	Run: func(cmd *cobra.Command, args []string) {
		runListConversations()
	},
}

func runArchive() {
	// NotifyContext returns a context canceled on Ctrl+C or SIGTERM, so
	// long-running HTTP calls can abort cleanly instead of hanging forever.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	auth := getMattermostAuthConfig()
	s3 := getS3Config()
	loc := getTimezone()

	encryptor, err := getEncryptor()
	if err != nil {
		printError("%v", err)
		os.Exit(1)
	}

	channelFlag := viper.GetString("archive.conversation")
	if channelFlag == "" {
		channelFlag = viper.GetString("archive.channel")
	}
	teamFlag := viper.GetString("archive.team")
	periodFlag := viper.GetString("archive.period")

	startMs, endMs := int64(0), int64(0)
	if periodFlag != "" {
		var err error
		startMs, endMs, err = parsePeriod(periodFlag, loc)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}
		fmt.Printf("Period:   %s (UTC)\n", periodFlag)
	}

	mattermostClient, me, err := connectMattermost(ctx, auth)
	if err != nil {
		printError("%v", err)
		os.Exit(1)
	}

	teams, err := mattermostClient.GetTeamsForUser(ctx, me.Id)
	if err != nil {
		printError("failed to list teams: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Authenticated as %s (%d teams)\n", me.Username, len(teams))

	userCache := newUserCache(mattermostClient)
	buffer := archive.NewDayBuffer()
	mdBuffer := archive.NewMarkdownBuffer()
	interrupted := false

	// Target a specific channel (by ID, or by name+team).
	if channelFlag != "" {
		var channel *mattermost.Channel
		teamName := ""

		if looksLikeChannelID(channelFlag) {
			channel, err = mattermostClient.GetChannel(ctx, channelFlag)
			if err != nil {
				printError("failed to resolve channel %q: %v", channelFlag, err)
				os.Exit(1)
			}
			if team, err := findTeamByID(teams, channel.TeamId); err == nil {
				teamName = team.Name
			}
		} else {
			if teamFlag == "" {
				printError("--conversation by name requires --team (or pass the conversation ID from 'mattermost list-conversations')")
				os.Exit(1)
			}
			team, err := findTeamByName(teams, teamFlag)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			channel, err = mattermostClient.GetChannelByName(ctx, team.Id, channelFlag)
			if err != nil {
				printError("failed to resolve channel %q in team %q: %v", channelFlag, teamFlag, err)
				os.Exit(1)
			}
			teamName = team.Name
		}

		meta, err := conversationMeta(ctx, mattermostClient, userCache, me.Id, *channel)
		if err != nil {
			printError("failed to resolve conversation participants: %v", err)
			os.Exit(1)
		}

		postCount, err := archiveChannel(ctx, mattermostClient, userCache, buffer, mdBuffer, startMs, endMs, loc, teamName, meta, *channel, progressFunc(channel.Name))
		if err != nil {
			if ctx.Err() != nil {
				interrupted = true
			} else {
				printError("failed to archive channel %q: %v", channel.Name, err)
				os.Exit(1)
			}
		}
		fmt.Fprintln(os.Stderr)
		if !interrupted {
			fmt.Printf("  channel %q: %d posts\n", channel.Name, postCount)
		}
	} else {
		if teamFlag != "" {
			team, err := findTeamByName(teams, teamFlag)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			teams = []mattermost.Team{*team}
		}

		for _, team := range teams {
			channels := []mattermost.Channel{}
			for _, fetch := range []func(int, int) ([]mattermost.Channel, error){
				func(page, perPage int) ([]mattermost.Channel, error) {
					return mattermostClient.GetPublicChannelsForTeam(ctx, team.Id, page, perPage)
				},
				func(page, perPage int) ([]mattermost.Channel, error) {
					return mattermostClient.GetPrivateChannelsForTeam(ctx, team.Id, page, perPage)
				},
			} {
				for page := 0; ; page++ {
					pageChannels, err := fetch(page, perPage)
					if err != nil {
						printError("failed to list channels of team %q: %v", team.Name, err)
						os.Exit(1)
					}
					channels = append(channels, pageChannels...)
					if len(pageChannels) < perPage {
						break
					}
				}
			}

			fmt.Printf("  team %q: %d channels\n", team.Name, len(channels))

			for _, channel := range channels {
				displayName := channel.DisplayName
				if displayName == "" {
					displayName = channel.Name
				}
				meta := archive.ConversationMeta{
					Type:        channel.Type,
					DisplayName: displayName,
					ChannelName: channel.Name,
				}
				postCount, err := archiveChannel(ctx, mattermostClient, userCache, buffer, mdBuffer, startMs, endMs, loc, team.Name, meta, channel, progressFunc(channel.Name))
				if err != nil {
					if ctx.Err() != nil {
						interrupted = true
						break
					}
					printError("failed to archive channel %q: %v", channel.Name, err)
					os.Exit(1)
				}
				fmt.Fprintln(os.Stderr)
				fmt.Printf("    channel %q: %d posts\n", channel.Name, postCount)
			}
			if interrupted {
				break
			}
		}
	}

	if interrupted {
		fmt.Fprintln(os.Stderr)
		fmt.Println("Interrupted, uploading the data fetched so far…")
	}

	if isDryRun() {
		fmt.Println("Dry run: no upload performed")
		for _, day := range buffer.Days() {
			key, err := archive.DailyObjectKey(day)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			if encryptor != nil {
				key += ".age"
			}
			fmt.Printf("  would upload %s (%d lines)\n", key, buffer.LineCount(day))
		}
		for _, key := range mdBuffer.Keys() {
			teamName, displayName, month, err := archive.ParseMonthKey(key)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			objKey, err := archive.MarkdownObjectKey(teamName, displayName, month)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			if encryptor != nil {
				objKey += ".age"
			}
			fmt.Printf("  would upload markdown %s (%d posts)\n", objKey, mdBuffer.LineCount(key))
		}
		if interrupted {
			os.Exit(130)
		}
		return
	}

	// After an interrupt the context is already canceled, so use a fresh one
	// for the remaining S3 uploads.
	uploadCtx := ctx
	if interrupted {
		uploadCtx = context.WithoutCancel(ctx)
	}

	var uploader archive.ObjectPutter
	uploader, err = archive.NewUploader(uploadCtx, s3.Endpoint, s3.AccessKey, s3.SecretKey, s3.Bucket, s3.UseSSL)
	if err != nil {
		printError("failed to connect to object storage: %v", err)
		os.Exit(1)
	}
	if encryptor != nil {
		uploader = archive.NewEncryptingUploader(uploader, encryptor)
	}

	if err := buffer.Flush(uploadCtx, uploader); err != nil {
		printError("failed to upload: %v", err)
		os.Exit(1)
	}

	if err := mdBuffer.Flush(uploadCtx, uploader); err != nil {
		printError("failed to upload markdown: %v", err)
		os.Exit(1)
	}

	fmt.Println("Upload complete")

	if interrupted {
		os.Exit(130)
	}
}

// connectMattermost authenticates against the Mattermost server and returns
// the client and the current user.
func connectMattermost(ctx context.Context, auth mattermostAuthConfig) (*mattermost.Client, *mattermost.User, error) {
	if auth.ServerURL == "" {
		return nil, nil, fmt.Errorf("mattermost server URL is required (flag --server-url, env MM_SERVER_URL, or config)")
	}

	client := mattermost.NewClient(auth.ServerURL)
	if err := client.Authenticate(ctx, mattermost.AuthConfig{
		Token:    auth.Token,
		Username: auth.Username,
		Password: auth.Password,
		MFAToken: auth.MFAToken,
	}); err != nil {
		return nil, nil, fmt.Errorf("authentication failed: %w", err)
	}

	me, err := client.GetMe(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get current user: %w", err)
	}

	return client, me, nil
}

func runTest() {
	// NotifyContext returns a context canceled on Ctrl+C or SIGTERM, so
	// long-running HTTP calls can abort cleanly instead of hanging forever.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	auth := getMattermostAuthConfig()

	mattermostClient, me, err := connectMattermost(ctx, auth)
	if err != nil {
		printError("%v", err)
		os.Exit(1)
	}

	teams, err := mattermostClient.GetTeamsForUser(ctx, me.Id)
	if err != nil {
		printError("failed to list teams: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Server:   %s\n", auth.ServerURL)
	fmt.Printf("Username: %s\n", me.Username)
	fmt.Printf("User ID:  %s\n", me.Id)
	fmt.Printf("Email:    %s\n", me.Email)
	fmt.Printf("Teams:    %d\n", len(teams))
}

func runListConversations() {
	// NotifyContext returns a context canceled on Ctrl+C or SIGTERM, so
	// long-running HTTP calls can abort cleanly instead of hanging forever.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	auth := getMattermostAuthConfig()

	mattermostClient, me, err := connectMattermost(ctx, auth)
	if err != nil {
		printError("%v", err)
		os.Exit(1)
	}

	teams, err := mattermostClient.GetTeamsForUser(ctx, me.Id)
	if err != nil {
		printError("failed to list teams: %v", err)
		os.Exit(1)
	}
	teamNames := make(map[string]string, len(teams))
	for _, t := range teams {
		teamNames[t.Id] = t.Name
	}

	channels, err := mattermostClient.GetChannelsForUser(ctx, me.Id)
	if err != nil {
		printError("failed to list channels: %v", err)
		os.Exit(1)
	}

	// Resolve participants of direct/group channels in one batch request.
	dmIDs := []string{}
	for _, ch := range channels {
		if ch.Type == "D" || ch.Type == "G" {
			dmIDs = append(dmIDs, ch.Id)
		}
	}
	participants := map[string][]mattermost.User{}
	if len(dmIDs) > 0 {
		participants, err = mattermostClient.GetUsersByGroupChannelIds(ctx, dmIDs)
		if err != nil {
			printError("failed to resolve channel participants: %v", err)
			os.Exit(1)
		}
	}

	// Direct (D) channels have no server-side participant resolution via
	// /users/group_channels (the server only handles group channels), so their
	// interlocutor is resolved via channel members instead.
	cache := newUserCache(mattermostClient)
	directInterlocutors := map[string]string{}
	for _, ch := range channels {
		if ch.Type == "D" {
			directInterlocutors[ch.Id] = resolveDirectInterlocutor(ctx, mattermostClient, cache, me.Id, ch.Id)
		}
	}

	// Render the channels table. Rows are collected first, then column widths
	// are computed so the table stays aligned even when a value exceeds a
	// fixed-width format.
	type row struct {
		team, typeLabel, name, display, id string
	}
	rows := make([]row, 0, len(channels))
	for _, ch := range channels {
		teamName := teamNames[ch.TeamId]
		if teamName == "" {
			teamName = "-"
		}
		typeLabel := channelTypeLabel(ch.Type)
		name := ch.Name
		if ch.Type == "D" || ch.Type == "G" {
			name = "-"
		}

		display := ch.DisplayName
		switch ch.Type {
		case "G":
			if users, ok := participants[ch.Id]; ok {
				usernames := make([]string, 0, len(users))
				for _, u := range users {
					usernames = append(usernames, "@"+u.Username)
				}
				if len(usernames) == 0 {
					display = "empty channel"
				} else {
					display = joinStrings(usernames, ", ")
				}
			} else {
				display = "(participants unknown)"
			}
		case "D":
			display = directInterlocutors[ch.Id]
			if display == "" {
				display = "(participants unknown)"
			}
		}

		rows = append(rows, row{teamName, typeLabel, name, display, ch.Id})
	}

	const (
		maxTeam    = 20
		maxType    = 8
		maxName    = 32
		maxDisplay = 56
	)

	widths := []int{len("TEAM"), len("TYPE"), len("NAME"), len("DISPLAY/PARTICIPANTS"), len("ID")}
	for _, r := range rows {
		widths[0] = max(widths[0], min(len(r.team), maxTeam))
		widths[1] = max(widths[1], min(len(r.typeLabel), maxType))
		widths[2] = max(widths[2], min(len(r.name), maxName))
		widths[3] = max(widths[3], min(len(r.display), maxDisplay))
		widths[4] = max(widths[4], len(r.id))
	}

	truncate := func(s string, w int) string {
		if len(s) > w {
			return s[:max(0, w-1)] + "…"
		}
		return s
	}

	fmt.Fprintf(os.Stdout, "%-*s  %-*s  %-*s  %-*s  %s\n",
		widths[0], "TEAM", widths[1], "TYPE", widths[2], "NAME", widths[3], "DISPLAY/PARTICIPANTS", "ID")
	for _, r := range rows {
		fmt.Fprintf(os.Stdout, "%-*s  %-*s  %-*s  %-*s  %s\n",
			widths[0], truncate(r.team, maxTeam),
			widths[1], truncate(r.typeLabel, maxType),
			widths[2], truncate(r.name, maxName),
			widths[3], truncate(r.display, maxDisplay),
			r.id)
	}
}

// resolveDirectInterlocutor returns the username of the other participant of
// a direct (1-1) channel, or a fallback if it cannot be resolved.
func resolveDirectInterlocutor(ctx context.Context, client *mattermost.Client, cache *userCache, meID, channelID string) string {
	members, err := client.GetChannelMembers(ctx, channelID, 0, 100)
	if err != nil {
		return "(participants unknown)"
	}

	interlocutors := []string{}
	for _, m := range members {
		if m.UserId == "" || m.UserId == meID {
			continue
		}
		interlocutors = append(interlocutors, "@"+cache.Resolve(ctx, m.UserId))
	}

	if len(interlocutors) == 0 {
		return "empty channel"
	}
	return joinStrings(interlocutors, ", ")
}

// conversationMeta resolves the metadata needed to render the Markdown file
// for a conversation. Public/private channels use their display name; direct
// and group channels use their participants (sorted, without '@').
func conversationMeta(ctx context.Context, client *mattermost.Client, cache *userCache, meID string, ch mattermost.Channel) (archive.ConversationMeta, error) {
	meta := archive.ConversationMeta{
		Type:        ch.Type,
		ChannelName: ch.Name,
		DisplayName: ch.DisplayName,
	}
	if meta.DisplayName == "" {
		meta.DisplayName = ch.Name
	}

	if ch.Type != "D" && ch.Type != "G" {
		return meta, nil
	}

	members, err := client.GetChannelMembers(ctx, ch.Id, 0, 100)
	if err != nil {
		return meta, err
	}

	usernames := []string{}
	for _, m := range members {
		if m.UserId == "" || m.UserId == meID {
			continue
		}
		usernames = append(usernames, cache.Resolve(ctx, m.UserId))
	}
	sort.Strings(usernames)
	meta.Participants = usernames

	// DisplayName for the object path keeps the '@' form.
	withAt := make([]string, len(usernames))
	for i, u := range usernames {
		withAt[i] = "@" + u
	}
	if len(withAt) == 0 {
		meta.DisplayName = "empty channel"
	} else {
		meta.DisplayName = joinStrings(withAt, ", ")
	}
	return meta, nil
}

func channelTypeLabel(t string) string {
	switch t {
	case "O":
		return "public"
	case "P":
		return "private"
	case "D":
		return "direct"
	case "G":
		return "group"
	default:
		return t
	}
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += sep
		}
		out += item
	}
	return out
}

func archiveChannel(
	ctx context.Context,
	client *mattermost.Client,
	userCache *userCache,
	buffer *archive.DayBuffer,
	mdBuffer *archive.MarkdownBuffer,
	startMs, endMs int64,
	loc *time.Location,
	teamName string,
	meta archive.ConversationMeta,
	channel mattermost.Channel,
	progress func(fetched int, day string),
) (int, error) {
	postCount := 0
	filtered := startMs != 0 || endMs != 0
	stopPagination := false
	lastDay := ""

	for page := 0; !stopPagination; page++ {
		list, err := client.GetPostsForChannel(ctx, channel.Id, page, perPage)
		if err != nil {
			return postCount, err
		}

		for _, postID := range list.Order {
			post := list.Posts[postID]
			if post == nil {
				continue
			}

			if filtered {
				// Posts are returned newest first, so once we hit a post older
				// than the start of the period we can stop entirely.
				if post.CreateAt < startMs {
					stopPagination = true
					break
				}
				if post.CreateAt > endMs {
					continue
				}
			}

			lastDay = time.UnixMilli(post.CreateAt).In(loc).Format("2006-01-02")

			username := userCache.Resolve(ctx, post.UserId)
			entry := archive.NormalizePost(post, archive.ChannelContext{
				TeamName:    teamName,
				ChannelName: channel.Name,
				ServerURL:   client.ServerURL(),
			}, loc)
			entry.Author = username

			if err := buffer.Add(entry); err != nil {
				return postCount, err
			}
			if mdBuffer != nil {
				if err := mdBuffer.Add(entry, meta); err != nil {
					return postCount, err
				}
			}
			postCount++
		}

		if progress != nil {
			progress(postCount, lastDay)
		}

		if len(list.Order) < perPage {
			break
		}
	}
	return postCount, nil
}

func isDryRun() bool {
	return viper.GetBool("dry-run")
}

// getEncryptor returns an Encryptor when encryption is enabled, or nil
// otherwise. The age recipient is required and validated as early as possible
// so a misconfigured run fails before any data is fetched.
func getEncryptor() (*archive.Encryptor, error) {
	if !viper.GetBool("age.encrypt") {
		return nil, nil
	}
	recipient := viper.GetString("age.recipient")
	if recipient == "" {
		return nil, fmt.Errorf("--encrypt requires an age recipient (flag --age-recipient, env AGE_RECIPIENT, or config [age].recipient)")
	}
	return archive.NewEncryptor(recipient)
}

// progressFunc returns a progress callback that updates a single line on
// stderr (via carriage return) as posts are fetched for a channel. Writing to
// stderr keeps stdout clean for the final results.
func progressFunc(channelName string) func(fetched int, day string) {
	return func(fetched int, day string) {
		if day != "" {
			fmt.Fprintf(os.Stderr, "\r  archiving %q… %d posts (%s)", channelName, fetched, day)
		} else {
			fmt.Fprintf(os.Stderr, "\r  archiving %q… %d posts", channelName, fetched)
		}
	}
}

// parsePeriod converts a period expression into a [start, end] range of
// Unix milliseconds, in the given location (a day/month/year boundary is
// midnight local time). Supported formats:
//   - YYYY-MM-DD: a single day
//   - YYYY-MM: a whole month
//   - YYYY: a whole year
func parsePeriod(s string, loc *time.Location) (int64, int64, error) {
	layouts := []string{
		"2006-01-02", // day
		"2006-01",    // month
		"2006",       // year
	}
	ends := []func(time.Time) time.Time{
		func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }, // day: +1 day
		func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }, // month: +1 month
		func(t time.Time) time.Time { return t.AddDate(1, 0, 0) }, // year: +1 year
	}

	for i, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			startT := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			endT := ends[i](startT)
			return startT.UnixMilli(), endT.UnixMilli() - 1, nil
		}
	}

	return 0, 0, fmt.Errorf("invalid period %q: expected YYYY-MM-DD, YYYY-MM, or YYYY", s)
}

// looksLikeChannelID reports whether the argument is a Mattermost channel ID
// (a 26-character base-32 string), as opposed to a channel name.
func looksLikeChannelID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789", r) {
			return false
		}
	}
	return true
}

func findTeamByName(teams []mattermost.Team, name string) (*mattermost.Team, error) {
	for i := range teams {
		if teams[i].Name == name {
			return &teams[i], nil
		}
	}
	return nil, fmt.Errorf("team %q not found in the teams accessible to the authenticated user", name)
}

func findTeamByID(teams []mattermost.Team, id string) (*mattermost.Team, error) {
	for i := range teams {
		if teams[i].Id == id {
			return &teams[i], nil
		}
	}
	return nil, fmt.Errorf("team with ID %q not found", id)
}

// userCache resolves user IDs to usernames with a local cache,
// batching unknown IDs into a single POST /users/ids call.
type userCache struct {
	client    *mattermost.Client
	usernames map[string]string
}

func newUserCache(client *mattermost.Client) *userCache {
	return &userCache{
		client:    client,
		usernames: make(map[string]string),
	}
}

// Resolve returns the username for a user ID, fetching and caching it on a miss.
func (c *userCache) Resolve(ctx context.Context, userID string) string {
	if username, ok := c.usernames[userID]; ok {
		return username
	}

	users, err := c.client.GetUsersByIds(ctx, []string{userID})
	if err != nil {
		return userID
	}
	for _, u := range users {
		c.usernames[u.Id] = u.Username
	}

	if username, ok := c.usernames[userID]; ok {
		return username
	}
	return userID
}
