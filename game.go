package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// ---------------------------------------------------------------- hub ---

type Hub struct {
	mu        sync.Mutex
	rooms     map[string]*Room
	questions []Question
	board     Board
	cfg       Config
}

func NewHub(questions []Question, board Board, cfg Config) *Hub {
	h := &Hub{rooms: map[string]*Room{}, questions: questions, board: board, cfg: cfg}
	go h.reapIdleRooms()
	return h
}

func (h *Hub) reapIdleRooms() {
	for range time.Tick(10 * time.Minute) {
		h.mu.Lock()
		for code, r := range h.rooms {
			r.mu.Lock()
			idle := time.Since(r.lastActivity) > roomIdleTimeout
			r.mu.Unlock()
			if idle {
				r.close()
				delete(h.rooms, code)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) getRoom(code string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[code]
	if !ok {
		return nil, fmt.Errorf("no room %q — ask the host for the code", code)
	}
	return r, nil
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I, O, 0, 1

func (h *Hub) CreateRoom(r *http.Request) (any, error) {
	var body struct {
		Host string `json:"host"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body) // optional
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var code string
	for {
		code = ""
		for i := 0; i < 4; i++ {
			code += string(codeAlphabet[rand.Intn(len(codeAlphabet))])
		}
		if _, taken := h.rooms[code]; !taken {
			break
		}
	}
	room := newRoom(code, h.questions, h.board, h.cfg)
	h.rooms[code] = room
	return map[string]string{"code": code, "hostId": room.hostID}, nil
}

func (h *Hub) Join(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	name := body.Name
	if len(name) > 24 {
		name = name[:24]
	}
	return room.join(name)
}

func (h *Hub) State(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	return room.publicState(), nil
}

func (h *Hub) Events(w http.ResponseWriter, r *http.Request) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	room.serveEvents(w, r)
}

func (h *Hub) Answer(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	var body struct {
		PlayerID string `json:"playerId"`
		OptionID int    `json:"optionId"`
		Text     string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("bad request body")
	}
	return nil, room.answer(body.PlayerID, body.OptionID, body.Text)
}

func (h *Hub) HostAction(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	var body struct {
		HostID string `json:"hostId"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.HostID != room.hostID {
		return nil, fmt.Errorf("not the host")
	}
	switch r.PathValue("action") {
	case "start":
		return nil, room.start()
	case "next":
		return nil, room.next()
	case "end":
		return nil, room.end()
	default:
		return nil, fmt.Errorf("unknown host action %q", r.PathValue("action"))
	}
}

// GradingQueue returns the open-format round awaiting grades: the
// question with its rubric plus every player's submitted text. Host-only.
func (h *Hub) GradingQueue(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	return room.gradingQueue(r.URL.Query().Get("hostId"))
}

// Grade records one player's grade for the current open-format round.
// Host-only; typically called by the host's Claude session acting as
// game master (see skills/quest-gamemaster).
func (h *Hub) Grade(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	var body struct {
		HostID   string `json:"hostId"`
		PlayerID string `json:"playerId"`
		Gained   int    `json:"gained"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("bad request body")
	}
	return nil, room.grade(body.HostID, body.PlayerID, body.Gained, body.Feedback)
}

func (h *Hub) CompleteGrading(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	var body struct {
		HostID string `json:"hostId"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	return nil, room.completeGrading(body.HostID)
}

func (h *Hub) HostView(r *http.Request) (any, error) {
	room, err := h.getRoom(r.PathValue("code"))
	if err != nil {
		return nil, err
	}
	return room.hostView(r.URL.Query().Get("hostId"))
}

// --------------------------------------------------------------- room ---

type Player struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Score         int    `json:"score"`
	Tile          int    `json:"tile"`
	Streak        int    `json:"streak"`
	Answered      bool   `json:"answered"`
	LastCorrect   bool   `json:"lastCorrect"`
	LastGained    int    `json:"lastGained"`
	LastFeedback  string `json:"lastFeedback,omitempty"`
	choice        int    // internal: option index chosen this round (mcq)
	answerText    string // internal: submitted text (open formats)
	graded        bool   // internal: grade recorded for this round
	gradeGained   int    // internal: points awarded by the grader
	gradeFeedback string // internal: grader feedback shown at reveal
}

type Room struct {
	Code   string
	hostID string

	mu           sync.Mutex
	players      map[string]*Player
	order        []string // join order
	phase        string   // lobby | question | grading | reveal | finished
	deck         []Question
	round        int // index into deck
	deadline     time.Time
	optionOrder  []int // display order -> original option index
	reveal       *revealInfo
	subs         map[chan []byte]struct{}
	board        Board
	cfg          Config
	lastActivity time.Time
	winner       string
}

type revealInfo struct {
	Format    string         `json:"format"`
	CorrectID int            `json:"correctId"` // -1 for open formats
	Why       string         `json:"why"`
	Source    string         `json:"source"`
	Results   []playerResult `json:"results"`
}

type playerResult struct {
	Name     string `json:"name"`
	Correct  bool   `json:"correct"`
	Gained   int    `json:"gained"`
	Feedback string `json:"feedback,omitempty"`
}

func newRoom(code string, questions []Question, board Board, cfg Config) *Room {
	deck := make([]Question, 0, len(questions))
	for _, q := range questions {
		if !q.playable() {
			continue
		}
		if len(cfg.Game.Formats) > 0 && !contains(cfg.Game.Formats, q.Format) {
			continue
		}
		deck = append(deck, q)
	}
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	n := cfg.Game.Rounds
	if n <= 0 || n > len(deck) {
		n = len(deck)
	}
	return &Room{
		Code:         code,
		hostID:       randomID("host"),
		players:      map[string]*Player{},
		phase:        "lobby",
		deck:         deck[:n],
		board:        board,
		cfg:          cfg,
		subs:         map[chan []byte]struct{}{},
		lastActivity: time.Now(),
	}
}

// playable means the question can run in the multiplayer engine:
// mcq auto-grades; the rubric formats go through the grading phase.
func (q Question) playable() bool {
	if q.Format == "mcq" {
		return len(q.Options) >= 2
	}
	return q.Rubric != nil && len(q.Rubric.MustCover) > 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (r *Room) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs {
		close(ch)
	}
}

func (r *Room) touch() { r.lastActivity = time.Now() }

func (r *Room) join(name string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	if r.phase != "lobby" {
		return nil, fmt.Errorf("the game already started")
	}
	id := randomID("p")
	r.players[id] = &Player{ID: id, Name: name}
	r.order = append(r.order, id)
	r.broadcast()
	return map[string]string{"playerId": id, "code": r.Code}, nil
}

func (r *Room) start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	if r.phase != "lobby" {
		return fmt.Errorf("game already started")
	}
	if len(r.players) == 0 {
		return fmt.Errorf("no players have joined yet")
	}
	r.round = 0
	r.beginRoundLocked()
	return nil
}

func (r *Room) beginRoundLocked() {
	q := r.deck[r.round]
	if q.Format == "mcq" {
		// Shuffle the option order so correct answers are not
		// positionally predictable across rooms.
		r.optionOrder = rand.Perm(len(q.Options))
	} else {
		r.optionOrder = nil
	}
	for _, p := range r.players {
		p.Answered, p.LastCorrect, p.LastGained, p.LastFeedback = false, false, 0, ""
		p.choice, p.answerText = -1, ""
		p.graded, p.gradeGained, p.gradeFeedback = false, 0, ""
	}
	r.reveal = nil
	r.phase = "question"
	r.deadline = time.Now().Add(time.Duration(r.cfg.Game.SecondsPerQuestion) * time.Second)
	r.broadcast()
	time.AfterFunc(time.Until(r.deadline), func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.phase == "question" && r.round < len(r.deck) {
			r.finishQuestionLocked()
		}
	})
}

func (r *Room) answer(playerID string, optionID int, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	if r.phase != "question" {
		return fmt.Errorf("no question is live")
	}
	p, ok := r.players[playerID]
	if !ok {
		return fmt.Errorf("unknown player")
	}
	if p.Answered {
		return fmt.Errorf("answer already locked in")
	}
	if time.Now().After(r.deadline) {
		return fmt.Errorf("time is up")
	}
	q := r.deck[r.round]
	if q.Format == "mcq" {
		if optionID < 0 || optionID >= len(q.Options) {
			return fmt.Errorf("no such option")
		}
		p.choice = optionID
	} else {
		if len(text) == 0 {
			return fmt.Errorf("an answer is required")
		}
		if len(text) > 8000 {
			text = text[:8000]
		}
		p.answerText = text
	}
	p.Answered = true
	if r.allAnsweredLocked() {
		r.finishQuestionLocked()
	} else {
		r.broadcast()
	}
	return nil
}

func (r *Room) allAnsweredLocked() bool {
	for _, id := range r.order {
		if !r.players[id].Answered {
			return false
		}
	}
	return true
}

// finishQuestionLocked ends the answering window: mcq rounds are graded
// immediately; rubric rounds wait in the grading phase for the game
// master (the host's Claude session, see skills/quest-gamemaster).
func (r *Room) finishQuestionLocked() {
	if r.deck[r.round].Format == "mcq" {
		r.evaluateLocked()
	} else {
		r.phase = "grading"
		r.broadcast()
	}
}

// evaluateLocked grades an mcq round, awards points, advances tiles, and
// moves to the reveal phase.
func (r *Room) evaluateLocked() {
	q := r.deck[r.round]
	correctID := -1
	for i, o := range q.Options {
		if o.Correct {
			correctID = i
			break
		}
	}
	total := float64(r.cfg.Game.SecondsPerQuestion)
	remaining := time.Until(r.deadline).Seconds()
	if remaining < 0 {
		remaining = 0
	}
	frac := 0.0
	if total > 0 {
		frac = remaining / total
	}

	rev := &revealInfo{Format: "mcq", CorrectID: correctID, Why: q.Why, Source: q.Source}
	for _, id := range r.order {
		p := r.players[id]
		res := playerResult{Name: p.Name}
		if p.Answered && p.choice == correctID {
			p.Streak++
			base := q.Depth * 10
			speed := int(float64(base) * 0.5 * frac)
			streakBonus := 2 * p.Streak
			if streakBonus > 10 {
				streakBonus = 10
			}
			res.Correct = true
			res.Gained = base + speed + streakBonus
			p.Score += res.Gained
			advance := 2
			if p.Streak >= 3 {
				advance = 3 // hot streak
			}
			p.Tile += advance
			if p.Tile > 24 {
				p.Tile = 24
			}
			if p.Tile == 24 && r.winner == "" {
				r.winner = p.Name
			}
		} else {
			p.Streak = 0
		}
		p.LastCorrect, p.LastGained = res.Correct, res.Gained
		rev.Results = append(rev.Results, res)
	}
	r.reveal = rev
	r.phase = "reveal"
	r.broadcast()
}

// gradingQueue exposes the submitted open-format answers with the
// rubric, for the game master. Answer texts are host-only.
func (r *Room) gradingQueue(hostID string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hostID != r.hostID {
		return nil, fmt.Errorf("not the host")
	}
	if r.phase != "grading" {
		return nil, fmt.Errorf("no round is awaiting grading (phase %q)", r.phase)
	}
	q := r.deck[r.round]
	type submission struct {
		PlayerID string `json:"playerId"`
		Name     string `json:"name"`
		Answered bool   `json:"answered"`
		Text     string `json:"text"`
		Graded   bool   `json:"graded"`
		Gained   int    `json:"gained"`
	}
	base := q.Depth * 10
	out := map[string]any{
		"round":      r.round,
		"totalRound": len(r.deck),
		"question": map[string]any{
			"id": q.ID, "topic": q.Topic, "depth": q.Depth, "format": q.Format,
			"text": q.Question, "base": base, "half": base / 2,
		},
		"rubric": q.Rubric,
		"why":    q.Why,
		"source": q.Source,
	}
	var subs []submission
	for _, id := range r.order {
		p := r.players[id]
		subs = append(subs, submission{
			PlayerID: p.ID, Name: p.Name, Answered: p.Answered,
			Text: p.answerText, Graded: p.graded, Gained: p.gradeGained,
		})
	}
	out["submissions"] = subs
	return out, nil
}

// grade records the game master's verdict for one player. gained is the
// points award (base for full credit, base/2 for half, 0 for a miss);
// full credit advances the token and feeds the streak.
func (r *Room) grade(hostID, playerID string, gained int, feedback string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	if hostID != r.hostID {
		return fmt.Errorf("not the host")
	}
	if r.phase != "grading" {
		return fmt.Errorf("no round is awaiting grading (phase %q)", r.phase)
	}
	p, ok := r.players[playerID]
	if !ok {
		return fmt.Errorf("unknown player")
	}
	if gained < 0 {
		return fmt.Errorf("gained must be >= 0")
	}
	if len(feedback) > 1000 {
		feedback = feedback[:1000]
	}
	p.graded = true
	p.gradeGained = gained
	p.gradeFeedback = feedback
	return nil
}

// completeGrading finalizes the open round: applies recorded grades
// (ungraded players score zero), advances tiles for full credit, and
// reveals.
func (r *Room) completeGrading(hostID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hostID != r.hostID {
		return fmt.Errorf("not the host")
	}
	if r.phase != "grading" {
		return fmt.Errorf("no round is awaiting grading (phase %q)", r.phase)
	}
	r.completeGradingLocked()
	return nil
}

func (r *Room) completeGradingLocked() {
	q := r.deck[r.round]
	base := q.Depth * 10
	rev := &revealInfo{Format: q.Format, CorrectID: -1, Why: q.Why, Source: q.Source}
	for _, id := range r.order {
		p := r.players[id]
		res := playerResult{Name: p.Name, Feedback: p.gradeFeedback}
		if p.graded {
			res.Gained = p.gradeGained
			p.Score += res.Gained
			if res.Gained >= base && base > 0 {
				// Full credit: tile advance and streak, like a
				// correct mcq answer (no speed bonus for typed
				// answers — typing speed is not the game).
				p.Streak++
				streakBonus := 2 * p.Streak
				if streakBonus > 10 {
					streakBonus = 10
				}
				res.Gained += streakBonus
				p.Score += streakBonus
				res.Correct = true
				advance := 2
				if p.Streak >= 3 {
					advance = 3
				}
				p.Tile += advance
				if p.Tile > 24 {
					p.Tile = 24
				}
				if p.Tile == 24 && r.winner == "" {
					r.winner = p.Name
				}
			} else {
				p.Streak = 0
			}
		} else {
			p.Streak = 0
		}
		p.LastCorrect, p.LastGained, p.LastFeedback = res.Correct, res.Gained, res.Feedback
		rev.Results = append(rev.Results, res)
	}
	r.reveal = rev
	r.phase = "reveal"
	r.broadcast()
}

func (r *Room) next() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	switch r.phase {
	case "reveal":
	case "question":
		r.finishQuestionLocked()
		return nil
	case "grading":
		// host skips grading: finalize with whatever grades exist
		r.completeGradingLocked()
		return nil
	case "finished":
		return nil
	default:
		return fmt.Errorf("nothing to advance")
	}
	if r.winner != "" || r.round+1 >= len(r.deck) {
		r.phase = "finished"
	} else {
		r.round++
		r.beginRoundLocked()
	}
	return nil
}

func (r *Room) end() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch()
	r.phase = "finished"
	r.broadcast()
	return nil
}

// ------------------------------------------------------------- wire ---

type wireQuestion struct {
	Topic   string `json:"topic"`
	Depth   int    `json:"depth"`
	Format  string `json:"format"`
	Text    string `json:"text"`
	Seconds int    `json:"seconds"`
	Options []struct {
		ID   int    `json:"id"`
		Text string `json:"text"`
	} `json:"options"`
}

type wirePlayer struct {
	Name         string `json:"name"`
	Score        int    `json:"score"`
	Tile         int    `json:"tile"`
	Streak       int    `json:"streak"`
	Answered     bool   `json:"answered"`
	LastCorrect  bool   `json:"lastCorrect"`
	LastGained   int    `json:"lastGained"`
	LastFeedback string `json:"lastFeedback,omitempty"`
}

type wireState struct {
	Code       string        `json:"code"`
	Phase      string        `json:"phase"`
	Round      int           `json:"round"`
	TotalRound int           `json:"totalRounds"`
	DeadlineMS int64         `json:"deadlineMs"`
	Winner     string        `json:"winner,omitempty"`
	Players    []wirePlayer  `json:"players"`
	Question   *wireQuestion `json:"question"`
	Reveal     *revealInfo   `json:"reveal,omitempty"`
	Board      Board         `json:"board"`
}

func (r *Room) publicState() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stateLocked()
}

func (r *Room) stateLocked() map[string]any {
	st := wireState{
		Code:       r.Code,
		Phase:      r.phase,
		Round:      r.round,
		TotalRound: len(r.deck),
		Winner:     r.winner,
		Board:      r.board,
	}
	for _, id := range r.order {
		p := r.players[id]
		st.Players = append(st.Players, wirePlayer{
			Name: p.Name, Score: p.Score, Tile: p.Tile, Streak: p.Streak,
			Answered: p.Answered, LastCorrect: p.LastCorrect, LastGained: p.LastGained,
			LastFeedback: p.LastFeedback,
		})
	}
	if r.phase == "question" || r.phase == "grading" || r.phase == "reveal" {
		q := r.deck[r.round]
		wq := &wireQuestion{Topic: q.Topic, Depth: q.Depth, Format: q.Format, Text: q.Question, Seconds: r.cfg.Game.SecondsPerQuestion}
		for _, orig := range r.optionOrder {
			wq.Options = append(wq.Options, struct {
				ID   int    `json:"id"`
				Text string `json:"text"`
			}{ID: orig, Text: q.Options[orig].Text})
		}
		st.Question = wq
		st.DeadlineMS = r.deadline.UnixMilli()
		if r.phase == "reveal" {
			st.Reveal = r.reveal
		}
	}
	b, _ := json.Marshal(st)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

// hostView adds the correct answer and rubric so the host console can
// preview and adjudicate. Requires the host ID.
func (r *Room) hostView(hostID string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hostID != r.hostID {
		return nil, fmt.Errorf("not the host")
	}
	out := r.stateLocked()
	if r.phase == "question" || r.phase == "grading" || r.phase == "reveal" {
		q := r.deck[r.round]
		correct := ""
		for _, o := range q.Options {
			if o.Correct {
				correct = o.Text
				break
			}
		}
		out["host"] = map[string]any{
			"id":      q.ID,
			"correct": correct,
			"why":     q.Why,
			"source":  q.Source,
			"rubric":  q.Rubric,
		}
	}
	return out, nil
}

// --------------------------------------------------------------- SSE ---

func (r *Room) serveEvents(w http.ResponseWriter, req *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan []byte, 32)
	r.mu.Lock()
	if r.subs == nil {
		r.mu.Unlock()
		return
	}
	r.subs[ch] = struct{}{}
	// Send the current state immediately so (re)connects are seamless.
	snapshot, _ := json.Marshal(r.stateLocked())
	r.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte("data: " + string(snapshot) + "\n\n"))
	fl.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-req.Context().Done():
			r.mu.Lock()
			delete(r.subs, ch)
			r.mu.Unlock()
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				r.mu.Lock()
				delete(r.subs, ch)
				r.mu.Unlock()
				return
			}
			fl.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write([]byte("data: " + string(msg) + "\n\n")); err != nil {
				r.mu.Lock()
				delete(r.subs, ch)
				r.mu.Unlock()
				return
			}
			fl.Flush()
		}
	}
}

func (r *Room) broadcast() {
	if r.subs == nil || len(r.subs) == 0 {
		return
	}
	snapshot, _ := json.Marshal(r.stateLocked())
	for ch := range r.subs {
		select {
		case ch <- snapshot:
		default: // slow consumer; it will catch up via state polling
		}
	}
}

func randomID(prefix string) string {
	const hex = "0123456789abcdef"
	s := prefix + "-"
	for i := 0; i < 12; i++ {
		s += string(hex[rand.Intn(16)])
	}
	return s
}
