package store_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func initStore(t *testing.T) (*store.Store, *testutil.Repo) {
	t.Helper()
	repo := testutil.NewRepo(t)
	s := store.Open(repo.Dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s, repo
}

func TestInitHidesTheFolderFromGit(t *testing.T) {
	s, repo := initStore(t)
	if !s.Exists() {
		t.Fatal("Exists() = false after Init")
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repo.Git("status", "--porcelain", "--untracked-files=all"); got != "" {
		t.Errorf("git status shows %q, want nothing from .turnback/", got)
	}
}

func TestTurnIDsKeepIncreasingAcrossReopens(t *testing.T) {
	s, repo := initStore(t)
	for want := 1; want <= 2; want++ {
		id, err := s.NextTurnID()
		if err != nil {
			t.Fatal(err)
		}
		if id != want {
			t.Fatalf("NextTurnID = %d, want %d", id, want)
		}
	}
	id, err := store.Open(repo.Dir).NextTurnID()
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 {
		t.Errorf("NextTurnID after reopening = %d, want 3", id)
	}
}

func TestTurnIDsNeverCollideWithSavedTurnsEvenIfStateIsLost(t *testing.T) {
	s, _ := initStore(t)
	saveTurn(t, s, 7)
	if err := os.Remove(filepath.Join(s.Dir, "state.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	id, err := s.NextTurnID()
	if err != nil {
		t.Fatal(err)
	}
	if id != 8 {
		t.Errorf("NextTurnID = %d, want 8", id)
	}
}

func saveTurn(t *testing.T, s *store.Store, id int) *store.Turn {
	t.Helper()
	turn := &store.Turn{
		ID:          id,
		Kind:        store.KindSession,
		Description: fmt.Sprintf("turn %d", id),
		StartedAt:   time.Date(2026, 9, 30, 14, 0, id, 0, time.UTC),
		EndedAt:     time.Date(2026, 9, 30, 14, 5, id, 0, time.UTC),
		Before:      "1111111111111111111111111111111111111111",
		After:       "2222222222222222222222222222222222222222",
		Files:       []store.File{{Path: "a.txt", Status: "M", Added: 1, Deleted: 1}},
	}
	if err := s.SaveTurn(turn, fmt.Sprintf("patch %d\n", id)); err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestSavedTurnsLoadBackWithEveryField(t *testing.T) {
	s, _ := initStore(t)
	want := &store.Turn{
		ID:          4,
		Kind:        store.KindUndo,
		Description: "Undo turn 2",
		Agent:       "cursor",
		StartedAt:   time.Date(2026, 9, 30, 14, 2, 11, 0, time.UTC),
		EndedAt:     time.Date(2026, 9, 30, 14, 22, 10, 0, time.UTC),
		Before:      "1111111111111111111111111111111111111111",
		After:       "2222222222222222222222222222222222222222",
		Files: []store.File{
			{Path: "src/client.go", Status: "M", Added: 21, Deleted: 7},
			{Path: "docs/new.md", OldPath: "docs/old.md", Status: "R"},
			{Path: "logo.png", Status: "A", Binary: true},
		},
		Undoes: &store.UndoInfo{Turn: 2, Paths: []string{"src/client.go"}},
	}
	if err := s.SaveTurn(want, "diff --git a/x b/x\n"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Turn(4)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(want.StartedAt) || !got.EndedAt.Equal(want.EndedAt) {
		t.Errorf("times = %v, %v; want %v, %v", got.StartedAt, got.EndedAt, want.StartedAt, want.EndedAt)
	}
	got.StartedAt, got.EndedAt = want.StartedAt, want.EndedAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded turn =\n%+v\nwant\n%+v", got, want)
	}
	patch, err := s.Patch(4)
	if err != nil {
		t.Fatal(err)
	}
	if patch != "diff --git a/x b/x\n" {
		t.Errorf("patch = %q", patch)
	}
}

func TestTurnsAreListedNewestFirst(t *testing.T) {
	s, _ := initStore(t)
	for _, id := range []int{2, 10, 1} {
		saveTurn(t, s, id)
	}
	turns, err := s.Turns()
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, turn := range turns {
		ids = append(ids, turn.ID)
	}
	if !reflect.DeepEqual(ids, []int{10, 2, 1}) {
		t.Errorf("ids = %v, want [10 2 1]", ids)
	}
}

func TestTurnsIsEmptyBeforeFirstUse(t *testing.T) {
	repo := testutil.NewRepo(t)
	turns, err := store.Open(repo.Dir).Turns()
	if err != nil || len(turns) != 0 {
		t.Errorf("Turns() = %v, %v; want no turns and no error", turns, err)
	}
}

func TestUnknownTurnIsNotFound(t *testing.T) {
	s, _ := initStore(t)
	if _, err := s.Turn(42); !errors.Is(err, store.ErrTurnNotFound) {
		t.Errorf("err = %v, want ErrTurnNotFound", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, _ := initStore(t)
	if got, err := s.Session(); err != nil || got != nil {
		t.Fatalf("Session() before start = %v, %v; want nil, nil", got, err)
	}
	want := &store.Session{
		StartedAt:   time.Date(2026, 9, 30, 9, 30, 0, 0, time.UTC),
		Snapshot:    "3333333333333333333333333333333333333333",
		Description: "refactor config",
		Agent:       "aider",
	}
	if err := s.SaveSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session()
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(want.StartedAt) || got.Snapshot != want.Snapshot ||
		got.Description != want.Description || got.Agent != want.Agent {
		t.Errorf("Session() = %+v, want %+v", got, want)
	}
	if err := s.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Session(); err != nil || got != nil {
		t.Errorf("Session() after clear = %v, %v; want nil, nil", got, err)
	}
}

func TestLockIsExclusive(t *testing.T) {
	s, _ := initStore(t)
	unlock, err := s.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(100 * time.Millisecond); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("second Lock err = %v, want ErrLocked", err)
	}
	unlock()
	unlock2, err := s.Lock(0)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}

func TestLockLeftByADeadProcessIsCleared(t *testing.T) {
	s, _ := initStore(t)
	dead := exec.Command("git", "--version")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("%d\n", dead.Process.Pid)
	if err := os.WriteFile(filepath.Join(s.Dir, "lock"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock(0)
	if err != nil {
		t.Fatalf("Lock with stale lock file: %v", err)
	}
	unlock()
}

func TestAStaleLockIsNeverHeldByTwoAtOnce(t *testing.T) {
	s, _ := initStore(t)
	for round := 0; round < 20; round++ {
		dead := exec.Command("git", "--version")
		if err := dead.Run(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Dir, "lock"), []byte(fmt.Sprintf("%d\n", dead.Process.Pid)), 0o644); err != nil {
			t.Fatal(err)
		}
		var holders, most int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unlock, err := s.Lock(0)
				if err != nil {
					return
				}
				n := atomic.AddInt32(&holders, 1)
				for {
					m := atomic.LoadInt32(&most)
					if n <= m || atomic.CompareAndSwapInt32(&most, m, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&holders, -1)
				unlock()
			}()
		}
		wg.Wait()
		if most > 1 {
			t.Fatalf("round %d: %d holders at once", round, most)
		}
	}
}

// TestHelperHoldLock is not a test: TestLockIsReleasedWhenItsHolderDies
// runs the test binary with it to hold a lock in another process.
func TestHelperHoldLock(t *testing.T) {
	dir := os.Getenv("TURNBACK_LOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	if _, err := (&store.Store{Dir: dir}).Lock(0); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Println("locked")
	time.Sleep(time.Minute)
}

func TestLockIsReleasedWhenItsHolderDies(t *testing.T) {
	s, _ := initStore(t)
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$")
	helper.Env = append(os.Environ(), "TURNBACK_LOCK_HELPER_DIR="+s.Dir)
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper said %q", line)
	}
	if _, err := s.Lock(0); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("Lock while another process holds it: err = %v, want ErrLocked", err)
	}
	helper.Process.Kill()
	helper.Wait()
	// Windows releases a dead process's locks shortly after it exits.
	unlock, err := s.Lock(5 * time.Second)
	if err != nil {
		t.Fatalf("Lock after the holder died: %v", err)
	}
	unlock()
}
