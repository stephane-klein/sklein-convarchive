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
	"github.com/stephane-klein/sklein-convarchive/pkg/ui"
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
	archiveCmd.Flags().String("period", "", "Period to archive: YYYY-MM (month) or YYYY (year)")

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

	userCache := newUserCache(mattermostClient)
	buffer := archive.NewMonthBuffer()
	mdBuffer := archive.NewMarkdownBuffer()
	interrupted := false
	flushedMonths := 0

	var uploader archive.ObjectPutter
	if !isDryRun() {
		uploader, err = archive.NewUploader(ctx, s3.Endpoint, s3.AccessKey, s3.SecretKey, s3.Bucket, s3.UseSSL)
		if err != nil {
			printError("failed to connect to object storage: %v", err)
			os.Exit(1)
		}
		if encryptor != nil {
			uploader = archive.NewEncryptingUploader(uploader, encryptor)
		}
	}

	d := ui.New(os.Stderr)
	if periodFlag != "" {
		fmt.Fprintf(os.Stderr, "Period to archive: %s\n", periodFlag)
	}

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

		meta, err := conversationMeta(ctx, mattermostClient, userCache, me.Id, *channel, teamName)
		if err != nil {
			printError("failed to resolve conversation participants: %v", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "Conversation to archive: %s\n", meta.DisplayName)

		convTask := d.Root(conversationTaskTitle(meta.DisplayName, teamName))
		_, err = archiveConversation(ctx, mattermostClient, userCache, buffer, mdBuffer, uploader, d, &flushedMonths, startMs, endMs, loc, meta, *channel, convTask)
		if err != nil {
			if ctx.Err() != nil {
				interrupted = true
			} else {
				d.Stop()
				printError("failed to archive channel %q: %v", channel.Name, err)
				os.Exit(1)
			}
		}
	} else {
		// List all channels the user is a member of (public, private, direct,
		// group). This only requires the user's own session, no system admin.
		channels, err := mattermostClient.GetChannelsForUser(ctx, me.Id)
		if err != nil {
			printError("failed to list channels: %v", err)
			os.Exit(1)
		}

		teamNames := make(map[string]string, len(teams))
		for _, t := range teams {
			teamNames[t.Id] = t.Name
		}

		if teamFlag != "" {
			team, err := findTeamByName(teams, teamFlag)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			// Restrict to the channels of that team (direct/group channels
			// have an empty team_id and are naturally excluded).
			teamChannels := []mattermost.Channel{}
			for _, ch := range channels {
				if ch.TeamId == team.Id {
					teamChannels = append(teamChannels, ch)
				}
			}
			channels = teamChannels
			fmt.Fprintf(os.Stderr, "Team to archive: %s (%d channels)\n", team.Name, len(channels))
		} else {
			fmt.Fprintf(os.Stderr, "Conversations to archive: %d\n", len(channels))
		}

		d.Start()

		for _, channel := range channels {
			meta, err := conversationMeta(ctx, mattermostClient, userCache, me.Id, channel, teamNames[channel.TeamId])
			if err != nil {
				printError("failed to resolve conversation metadata: %v", err)
				os.Exit(1)
			}

			convTask := d.Root(conversationTaskTitle(meta.DisplayName, teamNames[channel.TeamId]))
			_, err = archiveConversation(ctx, mattermostClient, userCache, buffer, mdBuffer, uploader, d, &flushedMonths, startMs, endMs, loc, meta, channel, convTask)
			if err != nil {
				if ctx.Err() != nil {
					interrupted = true
					break
				}
				d.Stop()
				printError("failed to archive channel %q: %v", channel.Name, err)
				os.Exit(1)
			}
		}
	}

	if interrupted {
		d.Stop()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Interrupted, fetching stopped")
	}

	if isDryRun() {
		d.Stop()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Dry run: no upload performed")
		for _, key := range buffer.Keys() {
			teamName, displayName, month, err := archive.ParseMonthKey(key)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			objKey, err := archive.JSONLObjectKey(teamName, displayName, month)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			if encryptor != nil {
				objKey += ".age"
			}
			fmt.Fprintf(os.Stderr, "  would upload %s (%d lines)\n", objKey, buffer.LineCount(key))
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
			fmt.Fprintf(os.Stderr, "  would upload markdown %s (%d posts)\n", objKey, mdBuffer.LineCount(key))
		}
		if interrupted {
			os.Exit(130)
		}
		return
	}

	if interrupted {
		fmt.Fprintf(os.Stderr, "Interrupted: %d complete month(s) already uploaded; the partially-fetched month was not\n", flushedMonths)
		os.Exit(130)
	}

	d.Stop()
}

// archiveConversation archives a single conversation and renders it as a task
// tree in the display: one child per pre-listed month, processed from the
// oldest to the newest. Each month is uploaded as soon as it is complete
// (incremental upload), so a partially-fetched month is never written to the
// object storage. Returns the number of archived posts.
func archiveConversation(
	ctx context.Context,
	client *mattermost.Client,
	userCache *userCache,
	buffer *archive.MonthBuffer,
	mdBuffer *archive.MarkdownBuffer,
	uploader archive.ObjectPutter, // nil in dry-run mode: nothing is uploaded
	display *ui.Display,
	flushedMonths *int,
	startMs, endMs int64,
	loc *time.Location,
	meta archive.ConversationMeta,
	channel mattermost.Channel,
	convTask *ui.Task,
) (int, error) {
	convTask.Status = ui.StatusRunning
	convTask.MaxVisibleChildren = 10
	convTask.AnchorFirstWhenPending = true
	display.Start()

	months, emptyText := planMonths(ctx, client, loc, channel, startMs, endMs)
	if emptyText != "" {
		convTask.Status = ui.StatusSuccess
		convTask.StatusText = emptyText
		display.Redraw()
		return 0, nil
	}

	monthTasks := make([]*ui.Task, 0, len(months))
	for _, m := range months {
		monthTasks = append(monthTasks, convTask.AddChild(meta.DisplayName+" "+m))
	}

	flushedSet := map[string]bool{}
	var currentMonth string
	allDone := false

	setMonths := func() {
		for i, t := range monthTasks {
			key := months[i]
			bufKey := archive.MonthKey(meta.TeamName, meta.DisplayName, key)
			switch {
			case allDone || key < currentMonth:
				t.Status = ui.StatusSuccess
				t.StatusText = monthStatusText(key, flushedSet, buffer.LineCount(bufKey) > 0)
			case key == currentMonth:
				t.Status = ui.StatusRunning
				t.StatusText = "… in progress …"
			default:
				t.Status = ui.StatusPending
				t.StatusText = ""
			}
		}
	}

	flushCompleted := func() error {
		if uploader == nil {
			return nil
		}
		for _, key := range months {
			if flushedSet[key] {
				continue
			}
			if !allDone && key >= currentMonth {
				continue
			}
			bufKey := archive.MonthKey(meta.TeamName, meta.DisplayName, key)
			if buffer.LineCount(bufKey) == 0 {
				continue
			}
			if err := buffer.FlushKey(ctx, uploader, bufKey); err != nil {
				return err
			}
			if err := mdBuffer.FlushKey(ctx, uploader, bufKey); err != nil {
				return err
			}
			flushedSet[key] = true
			*flushedMonths++
		}
		return nil
	}

	progress := func(fetched int, day string) error {
		if day != "" {
			currentMonth = day[:7]
		}
		convTask.StatusText = fmt.Sprintf("%d posts", fetched)
		if err := flushCompleted(); err != nil {
			return err
		}
		setMonths()
		display.Redraw()
		return nil
	}

	postCount, err := archiveChannel(ctx, client, userCache, buffer, mdBuffer, startMs, endMs, loc, meta, channel, progress)
	if err != nil {
		convTask.Status = ui.StatusError
		if ctx.Err() != nil {
			convTask.StatusText = "interrupted"
			for i, t := range monthTasks {
				if months[i] == currentMonth {
					t.Status = ui.StatusError
					t.StatusText = "interrupted"
				}
			}
		}
		display.Redraw()
		return postCount, err
	}

	allDone = true
	if err := flushCompleted(); err != nil {
		return postCount, err
	}
	setMonths()
	convTask.Status = ui.StatusSuccess
	if postCount > 0 {
		convTask.StatusText = "Ok"
	} else {
		convTask.StatusText = "no messages"
	}
	display.Redraw()
	return postCount, nil
}

// monthStatusText renders the trailing status of a completed month task.
// Months that were uploaded carry the "(uploaded)" marker; in dry-run mode
// nothing is uploaded and the marker is omitted; months without messages show
// "no messages".
func monthStatusText(month string, flushedSet map[string]bool, hasData bool) string {
	if flushedSet[month] {
		return "Ok (uploaded)"
	}
	if isDryRun() && hasData {
		return "Ok"
	}
	return "no messages"
}

// conversationTaskTitle prefixes the conversation display name with the team
// when the channel belongs to one, so homonymous channels (e.g. "general" in
// every team) stay distinguishable. Direct/group conversations have no team.
func conversationTaskTitle(displayName, teamName string) string {
	if teamName == "" {
		return displayName
	}
	return teamName + "/" + displayName
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
func conversationMeta(ctx context.Context, client *mattermost.Client, cache *userCache, meID string, ch mattermost.Channel, teamName string) (archive.ConversationMeta, error) {
	meta := archive.ConversationMeta{
		TeamName:    teamName,
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

	// Direct and group conversations have no team; the object path carries a
	// type label instead of a team slug.
	if ch.Type == "D" {
		meta.TeamName = "direct-messages"
	} else {
		meta.TeamName = "group-messages"
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
	buffer *archive.MonthBuffer,
	mdBuffer *archive.MarkdownBuffer,
	startMs, endMs int64,
	loc *time.Location,
	meta archive.ConversationMeta,
	channel mattermost.Channel,
	progress func(fetched int, day string) error,
) (int, error) {
	postCount := 0
	lastDay := ""

	err := client.PostsAscending(ctx, channel.Id, startMs, endMs, perPage,
		func() error {
			if progress != nil {
				return progress(postCount, lastDay)
			}
			return nil
		},
		func(post *mattermost.Post) error {
			lastDay = time.UnixMilli(post.CreateAt).In(loc).Format("2006-01-02")

			username := userCache.Resolve(ctx, post.UserId)
			entry := archive.NormalizePost(post, archive.ChannelContext{
				TeamName:    meta.TeamName,
				ChannelName: channel.Name,
				ServerURL:   client.ServerURL(),
			}, loc)
			entry.Author = username

			if err := buffer.Add(entry, meta); err != nil {
				return err
			}
			if mdBuffer != nil {
				if err := mdBuffer.Add(entry, meta); err != nil {
					return err
				}
			}
			postCount++
			return nil
		},
	)
	return postCount, err
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

// planMonths returns the ascending list of month keys to display for a
// channel, bounded by the month of its newest post and either the period
// start or the month of its oldest post (found by binary-searching the posts
// pagination). The second return value is a status text when there is nothing
// to list ("no messages" / "no messages in period"); an empty second value
// with a nil month list means the range could not be determined, in which
// case the caller degrades to a conversation-level task only.
func planMonths(ctx context.Context, client *mattermost.Client, loc *time.Location, ch mattermost.Channel, startMs, endMs int64) ([]string, string) {
	page0, err := client.GetPostsForChannel(ctx, ch.Id, 0, perPage)
	if err != nil {
		return nil, ""
	}
	if len(page0.Order) == 0 {
		return nil, "no messages"
	}
	newest := page0.Posts[page0.Order[0]]
	if newest == nil {
		return nil, ""
	}
	top := time.UnixMilli(newest.CreateAt).In(loc).Format("2006-01")

	bottom := ""
	if startMs != 0 {
		bottom = time.UnixMilli(startMs).In(loc).Format("2006-01")
		if endTop := time.UnixMilli(endMs).In(loc).Format("2006-01"); top > endTop {
			top = endTop
		}
	} else {
		oldest, err := client.GetOldestPost(ctx, ch.Id, perPage)
		if err != nil {
			return nil, ""
		}
		if oldest == nil {
			return nil, "no messages"
		}
		bottom = time.UnixMilli(oldest.CreateAt).In(loc).Format("2006-01")
	}

	if bottom > top {
		return nil, "no messages in period"
	}

	months := []string{}
	for m := bottom; m <= top; m = nextMonth(m) {
		months = append(months, m)
	}
	return months, ""
}

// nextMonth returns the month key following key ("2006-01").
func nextMonth(key string) string {
	t, err := time.Parse("2006-01", key)
	if err != nil {
		return key
	}
	return t.AddDate(0, 1, 0).Format("2006-01")
}

// parsePeriod converts a period expression into a [start, end] range of
// Unix milliseconds, in the given location (a month/year boundary is
// midnight local time). Supported formats:
//   - YYYY-MM: a whole month
//   - YYYY: a whole year
func parsePeriod(s string, loc *time.Location) (int64, int64, error) {
	layouts := []string{
		"2006-01", // month
		"2006",    // year
	}
	ends := []func(time.Time) time.Time{
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

	return 0, 0, fmt.Errorf("invalid period %q: expected YYYY-MM or YYYY", s)
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
