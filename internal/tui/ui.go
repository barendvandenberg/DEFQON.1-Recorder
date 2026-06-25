package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/revunix/defqon1-recorder/internal/config"
	"github.com/revunix/defqon1-recorder/internal/disk"
	"github.com/revunix/defqon1-recorder/internal/listener"
	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/recorder"
	"github.com/revunix/defqon1-recorder/internal/status"
	"github.com/revunix/defqon1-recorder/internal/timetable"
	"github.com/revunix/defqon1-recorder/internal/util"
	"github.com/revunix/defqon1-recorder/internal/youtube"
)

// State colors.
var (
	colorRecording = tcell.NewHexColor(0x00C853)
	colorOnline    = tcell.NewHexColor(0xFFD600)
	colorOffline   = tcell.NewHexColor(0x7A7A7A)
	colorSkip      = tcell.NewHexColor(0x00BFA5) // teal: recording disabled
)

// stageColors maps each DEFQON.1 stage to its real signature color.
var stageColors = map[string]tcell.Color{
	"RED":     tcell.NewHexColor(0xE10600),
	"BLUE":    tcell.NewHexColor(0x2E9BFF),
	"BLACK":   tcell.NewHexColor(0x9E9E9E), // gray, pure black is invisible
	"UV":      tcell.NewHexColor(0x9B5DE5),
	"MAGENTA": tcell.NewHexColor(0xE6007E),
	"INDIGO":  tcell.NewHexColor(0x5B5BD6),
	"GOLD":    tcell.NewHexColor(0xFFC400),
	"YELLOW":  tcell.NewHexColor(0xFFEA00),
	"GREEN":   tcell.NewHexColor(0x00E676),
	"PINK":    tcell.NewHexColor(0xFF6EC7),
	"PURPLE":  tcell.NewHexColor(0x8A2BE2),
	"ORANGE":  tcell.NewHexColor(0xFF7A00),
	"WHITE":   tcell.NewHexColor(0xF5F5F5),
	"SILVER":  tcell.NewHexColor(0xC0C0C0),
	"BROWN":   tcell.NewHexColor(0xB07A4F),
}

func stageColor(stage string) tcell.Color {
	if c, ok := stageColors[strings.ToUpper(strings.TrimSpace(stage))]; ok {
		return c
	}
	return tcell.ColorDefault
}

// RecordingGate decouples the UI from the controller: the UI can query and
// toggle whether a channel is recorded without depending on the controller
// package directly.
type RecordingGate interface {
	IsEnabled(channel string) bool
	Toggle(channel string) bool
}

type UI struct {
	app         *tview.Application
	root        *tview.Flex
	streamTbl   *tview.Table
	ttTable     *tview.Table
	ytTable     *tview.Table
	logView     *tview.TextView
	statusBar   *tview.TextView
	controlsBar *tview.TextView
	recorder    *recorder.Manager
	listener    *listener.Player
	youtube     *youtube.Manager
	gate        RecordingGate
	timetable   *timetable.Timetable
	streams     *status.Registry
	cfg         config.Config
	logCh       <-chan LogMessage
}

func New(
	cfg config.Config,
	rec *recorder.Manager,
	gate RecordingGate,
	tt *timetable.Timetable,
	streams *status.Registry,
	yt *youtube.Manager,
	logCh <-chan LogMessage,
	log logging.Logger,
) *UI {
	ui := &UI{
		app:       tview.NewApplication(),
		cfg:       cfg,
		recorder:  rec,
		listener:  listener.New(cfg.ToolsDir, log),
		youtube:   yt,
		gate:      gate,
		timetable: tt,
		streams:   streams,
		logCh:     logCh,
	}
	ui.build()
	return ui
}

func (u *UI) build() {
	u.streamTbl = newTable(" Streams ", true)
	u.ttTable = newTable(" Timetable ", false)
	if u.youtube != nil && u.youtube.Enabled() {
		u.ytTable = newTable(" YouTube ", true)
	}

	u.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true)
	u.logView.SetTitle(" Logs ").SetBorder(true)

	u.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	u.statusBar.SetTitle(" Status ").SetBorder(true)

	u.controlsBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(false)
	u.controlsBar.SetText(u.controlsHelp())
	u.controlsBar.SetTitle(" Controls ").SetBorder(true)

	top := tview.NewFlex().
		AddItem(u.streamTbl, 0, 1, false).
		AddItem(u.ttTable, 0, 1, false)

	logRow := tview.NewFlex().
		AddItem(u.logView, 0, 3, false)
	if u.ytTable != nil {
		logRow.AddItem(u.ytTable, 60, 1, false)
	}

	u.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 0, 8, false).
		AddItem(logRow, 0, 3, false).
		AddItem(u.statusBar, 0, 1, false).
		AddItem(u.controlsBar, 0, 1, false)

	u.app.SetRoot(u.root, true)
	u.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC || event.Rune() == 'q' {
			u.appendLog(LogMessage{Level: logging.LevelInfo, Text: "Stopping application; finalizing active recordings..."})
			go func() {
				time.Sleep(250 * time.Millisecond)
				u.app.Stop()
			}()
			return nil
		}
		if event.Rune() == 'l' || event.Rune() == 'L' {
			u.listenSelected()
			return nil
		}
		if event.Rune() == 's' || event.Rune() == 'S' {
			u.stopListening()
			return nil
		}
		if event.Rune() == 'd' || event.Rune() == 'D' {
			u.toggleRecordingSelected()
			return nil
		}
		if event.Key() == tcell.KeyTab && u.ytTable != nil {
			u.toggleFocus()
			return nil
		}
		if event.Rune() == 'v' || event.Rune() == 'V' {
			u.openYouTubeSelected(false)
			return nil
		}
		if event.Rune() == 'f' || event.Rune() == 'F' {
			u.openYouTubeSelected(true)
			return nil
		}
		if event.Key() == tcell.KeyEsc {
			u.stopYouTubePreview()
			return nil
		}
		return event
	})
	u.app.SetFocus(u.streamTbl)
}

// controlsHelp renders the keybinding legend shown in the controls bar.
func (u *UI) controlsHelp() string {
	key := func(k string) string { return fmt.Sprintf("[yellow::b]%s[-:-:-]", k) }
	help := "{l} Listen   {s} Stop audio   {d} Toggle recording"
	if u.ytTable != nil {
		help = "{l} Listen   {s} Stop audio   {d} Toggle rec/mode   {tab} Streams/YouTube   {v} mpv   {f} Fullscreen   {esc} Close mpv"
	}
	help += "   {nav} Select stream   {q} Quit"
	return strings.NewReplacer(
		"{l}", key("l"),
		"{s}", key("s"),
		"{d}", key("d"),
		"{tab}", key("Tab"),
		"{v}", key("v"),
		"{f}", key("f"),
		"{esc}", key("Esc"),
		"{nav}", key("\u2191/\u2193"),
		"{q}", key("q"),
	).Replace(help)
}

func newTable(title string, selectable bool) *tview.Table {
	t := tview.NewTable().
		SetBorders(false).
		SetSelectable(selectable, false)
	if selectable {
		t.SetFixed(1, 0)
	}
	t.SetTitle(title).SetBorder(true)
	return t
}

func (u *UI) Run() error {
	go u.drainLogs()
	return u.app.Run()
}

// Stop ends the event loop; safe to call from any goroutine (e.g. signal handler).
func (u *UI) Stop() {
	u.app.Stop()
}

func (u *UI) Close() {
	u.listener.Stop()
	if u.youtube != nil {
		u.youtube.StopPreview()
	}
}

func (u *UI) RunRefresh(ctx context.Context) {
	u.refresh()
	ticker := time.NewTicker(u.cfg.TUIUpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.refresh()
		}
	}
}

func (u *UI) refresh() {
	streamRows := u.buildStreamRows()
	ttRows := u.buildTimetableRows()
	ytRows := u.buildYouTubeRows()
	status := u.buildStatusBar()

	u.app.QueueUpdateDraw(func() {
		renderCells(u.streamTbl,
			[]string{"Stage", "Status", "Artist", "Listeners", "Size", "Ends in"},
			streamRows,
			true,
		)
		renderCells(u.ttTable,
			[]string{"Stage", "Time", "Artist", "Starts In"},
			ttRows,
			false,
		)
		if u.ytTable != nil {
			renderCells(u.ytTable,
				[]string{"Day", "Mode", "State", "Rec", "mpv", "Check"},
				ytRows,
				true,
			)
		}
		u.statusBar.SetText(status)
	})
}

func (u *UI) buildStreamRows() [][]cell {
	snaps := u.recorder.Active()
	byStage := make(map[string]recorder.Snapshot, len(snaps))
	for _, s := range snaps {
		byStage[s.Stage] = s
	}

	streams := u.streams.All()
	now := time.Now()

	rows := make([][]cell, 0, len(streams))
	for _, st := range streams {
		snap, active := byStage[st.Stage]

		disabled := !u.gate.IsEnabled(st.Channel)
		statusText := "Offline"
		statusColor := colorOffline
		switch {
		case disabled:
			statusText, statusColor = "Skip", colorSkip
		case active:
			statusText, statusColor = "Recording", colorRecording
		case st.Online:
			statusText, statusColor = "Online", colorOnline
		}

		artist := "-"
		endsIn := "-"
		if set := u.timetable.CurrentSet(st.Stage); set != nil {
			artist = set.DJ
			if diff := set.End.Sub(now); diff > 0 {
				endsIn = fmt.Sprintf("%d min", int(diff.Minutes()))
			} else {
				endsIn = "Ended"
			}
		}

		listeners := "-"
		if st.Online || active {
			listeners = util.FormatThousands(st.ListenerCount)
		}

		size := "-"
		if active {
			if info, err := os.Stat(snap.Path); err == nil {
				size = util.FormatBytes(info.Size())
			} else {
				size = "N/A"
			}
		}

		rows = append(rows, []cell{
			{util.Sanitize(st.Stage), stageColor(st.Stage)},
			{statusText, statusColor},
			{util.Sanitize(artist), tcell.ColorDefault},
			{listeners, tcell.ColorDefault},
			{util.Sanitize(size), tcell.ColorDefault},
			{util.Sanitize(endsIn), tcell.ColorDefault},
		})
	}
	return rows
}

func (u *UI) buildTimetableRows() [][]cell {
	upcoming := u.timetable.Upcoming()
	now := time.Now()
	loc := util.Berlin()

	rows := make([][]cell, 0, len(upcoming))
	for _, s := range upcoming {
		diff := int(s.Start.Sub(now).Minutes())
		rows = append(rows, []cell{
			{util.Sanitize(s.Stage), stageColor(s.Stage)},
			{s.Start.In(loc).Format("15:04"), tcell.ColorDefault},
			{util.Sanitize(s.DJ), tcell.ColorDefault},
			{util.FormatDuration(diff), tcell.ColorDefault},
		})
	}
	return rows
}

func (u *UI) buildYouTubeRows() [][]cell {
	if u.youtube == nil {
		return nil
	}
	snaps := u.youtube.Snapshot()
	rows := make([][]cell, 0, len(snaps))
	for _, s := range snaps {
		rec := "-"
		if s.Recording {
			rec = "Yes"
		}
		preview := "-"
		if s.Previewing {
			preview = "Open"
			if s.Fullscreen {
				preview = "Full"
			}
		}
		check := "-"
		if !s.LastChecked.IsZero() {
			check = s.LastChecked.In(util.Berlin()).Format("15:04")
		}
		rows = append(rows, []cell{
			{util.Sanitize(s.Name), tcell.ColorDefault},
			{util.Sanitize(s.Mode.Label()), youtubeModeColor(s.Mode)},
			{util.Sanitize(string(s.State)), youtubeStateColor(s.State)},
			{rec, tcell.ColorDefault},
			{preview, tcell.ColorDefault},
			{check, tcell.ColorDefault},
		})
	}
	return rows
}

func youtubeStateColor(state youtube.State) tcell.Color {
	switch state {
	case youtube.StateRecording:
		return colorRecording
	case youtube.StateTrailer, youtube.StateChecking, youtube.StateRecovering:
		return colorOnline
	case youtube.StateError:
		return tcell.ColorRed
	case youtube.StateDisabled, youtube.StateNotLive:
		return colorOffline
	default:
		return tcell.ColorDefault
	}
}

func youtubeModeColor(mode youtube.RecordMode) tcell.Color {
	switch mode {
	case youtube.ModeNone:
		return colorOffline
	case youtube.ModeAudio:
		return colorOnline
	default:
		return colorRecording
	}
}

func (u *UI) buildStatusBar() string {
	audio := u.listener.Snapshot()
	audioText := string(audio.State)
	hint := "(l)listen"
	if audio.Stage != "" {
		audioText = fmt.Sprintf("%s: %s", audio.State, audio.Stage)
	}
	if audio.State == listener.StatePlaying || audio.State == listener.StateStarting {
		hint = "(s)stop"
	}
	usage := disk.Scan(u.cfg.RecordingsDir)
	diskText := fmt.Sprintf("%s / %s free",
		util.FormatBytes(int64(usage.Total)), util.FormatBytes(int64(usage.VolumeFree)))
	return util.Sanitize(fmt.Sprintf(
		"DEFQON.1 Recorder by revunix | Active: %d/%d | Listeners: %s | Audio: %s %s | Disk: %s",
		u.recorder.Count(),
		len(u.cfg.Channels),
		util.FormatThousands(u.recorder.TotalListeners()),
		audioText,
		hint,
		diskText,
	))
}

func (u *UI) toggleFocus() {
	if u.ytTable == nil {
		return
	}
	if u.app.GetFocus() == u.ytTable {
		u.app.SetFocus(u.streamTbl)
		return
	}
	u.app.SetFocus(u.ytTable)
}

func (u *UI) listenSelected() {
	stream, ok := u.selectedStream()
	if !ok {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "No stream selected."})
		return
	}
	if !stream.Online || stream.StreamURL == "" {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: fmt.Sprintf("[%s] No live stream URL available.", stream.Stage)})
		return
	}

	stage := stream.Stage
	streamURL := stream.StreamURL
	u.appendLog(LogMessage{Level: logging.LevelInfo, Text: fmt.Sprintf("[%s] Starting TUI audio...", stage)})
	// The listener logs its own outcome (playing/failed/unexpected end) so the
	// caller does not need to interpret the return value.
	go func() {
		_ = u.listener.Play(stage, streamURL)
	}()
}

func (u *UI) stopListening() {
	audio := u.listener.Snapshot()
	if audio.State == listener.StateStopped {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "Audio is already stopped."})
		return
	}
	u.listener.Stop()
	u.appendLog(LogMessage{Level: logging.LevelInfo, Text: "TUI audio stopped."})
}

func (u *UI) openYouTubeSelected(fullscreen bool) {
	if u.youtube == nil || !u.youtube.PreviewSupported() {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "YouTube mpv preview is not available on this platform."})
		return
	}
	feed, ok := u.selectedYouTube()
	if !ok {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "No YouTube feed selected."})
		return
	}
	if err := u.youtube.OpenPreview(feed.Name, fullscreen); err != nil {
		u.appendLog(LogMessage{Level: logging.LevelError, Text: fmt.Sprintf("[YouTube %s] mpv preview failed: %s", feed.Name, err)})
	}
}

func (u *UI) stopYouTubePreview() {
	if u.youtube == nil || !u.youtube.PreviewSupported() {
		return
	}
	u.youtube.StopPreview()
}

func (u *UI) toggleRecordingSelected() {
	if u.ytTable != nil && u.app.GetFocus() == u.ytTable {
		u.toggleYouTubeModeSelected()
		return
	}
	stream, ok := u.selectedStream()
	if !ok {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "No stream selected."})
		return
	}
	enabled := u.gate.Toggle(stream.Channel)
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	u.appendLog(LogMessage{Level: logging.LevelInfo, Text: fmt.Sprintf("[%s] Recording %s.", stream.Stage, state)})
}

func (u *UI) toggleYouTubeModeSelected() {
	if u.youtube == nil {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "No YouTube feed selected."})
		return
	}
	feed, ok := u.selectedYouTube()
	if !ok {
		u.appendLog(LogMessage{Level: logging.LevelWarn, Text: "No YouTube feed selected."})
		return
	}
	mode, err := u.youtube.ToggleMode(feed.Name)
	if err != nil {
		u.appendLog(LogMessage{Level: logging.LevelError, Text: fmt.Sprintf("[YouTube %s] Toggle failed: %s", feed.Name, err)})
		return
	}
	u.appendLog(LogMessage{Level: logging.LevelInfo, Text: fmt.Sprintf("[YouTube %s] Recording mode: %s.", feed.Name, mode.Label())})
}

func (u *UI) selectedStream() (status.Stream, bool) {
	row, _ := u.streamTbl.GetSelection()
	streams := u.streams.All()
	if len(streams) == 0 {
		return status.Stream{}, false
	}
	if row < 1 {
		row = 1
	}
	idx := row - 1
	if idx >= len(streams) {
		idx = len(streams) - 1
	}
	u.streamTbl.Select(idx+1, 0)
	return streams[idx], true
}

func (u *UI) selectedYouTube() (youtube.Snapshot, bool) {
	if u.youtube == nil {
		return youtube.Snapshot{}, false
	}
	snaps := u.youtube.Snapshot()
	if len(snaps) == 0 {
		return youtube.Snapshot{}, false
	}
	row := 1
	if u.ytTable != nil {
		row, _ = u.ytTable.GetSelection()
	}
	if row < 1 {
		row = 1
	}
	idx := row - 1
	if idx >= len(snaps) {
		idx = len(snaps) - 1
	}
	if u.ytTable != nil {
		u.ytTable.Select(idx+1, 0)
	}
	return snaps[idx], true
}

func (u *UI) drainLogs() {
	for msg := range u.logCh {
		u.app.QueueUpdateDraw(func() {
			u.appendLog(msg)
		})
	}
}

func (u *UI) appendLog(msg LogMessage) {
	fmt.Fprintln(u.logView, formatLog(msg))
	u.logView.ScrollToEnd()
}

func formatLog(msg LogMessage) string {
	ts := time.Now().In(util.Berlin()).Format("15:04:05")
	switch msg.Level {
	case logging.LevelWarn:
		return fmt.Sprintf("%s [yellow::b]WARN[-:-:-] %s", ts, tview.Escape(msg.Text))
	case logging.LevelError:
		return fmt.Sprintf("%s [red::b]ERROR[-:-:-] %s", ts, tview.Escape(msg.Text))
	default:
		return fmt.Sprintf("%s %s", ts, tview.Escape(msg.Text))
	}
}

type cell struct {
	text  string
	color tcell.Color
}

func renderCells(t *tview.Table, headers []string, rows [][]cell, preserveSelection bool) {
	selectedRow, selectedCol := t.GetSelection()
	t.Clear()
	for c, h := range headers {
		t.SetCell(0, c, tview.NewTableCell(h).
			SetTextColor(tcell.ColorWhite).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}
	for r, row := range rows {
		for c, cl := range row {
			tc := tview.NewTableCell(cl.text).
				SetMaxWidth(50)
			if cl.color != tcell.ColorDefault {
				tc.SetTextColor(cl.color)
			}
			t.SetCell(r+1, c, tc)
		}
	}
	if preserveSelection && len(rows) > 0 {
		if selectedRow < 1 {
			selectedRow = 1
		}
		if selectedRow > len(rows) {
			selectedRow = len(rows)
		}
		t.Select(selectedRow, selectedCol)
	}
}
