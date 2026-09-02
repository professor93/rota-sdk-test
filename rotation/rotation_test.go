package rotation_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/rotation"
	"github.com/professor93/rota/store"
	"rotatest/internal/fake"
)

// acct is an account in the queue at order with one whole-account window
// at percent; a negative percent leaves it without a reading.
func acct(id, order int, percent float64) *rota.Account {
	a := &rota.Account{ID: id, Provider: "t-plain", Order: order}
	if percent >= 0 {
		a.Quota = &rota.Quota{Windows: []rota.Window{{Name: "5h", Percent: percent, Primary: true}}}
	}
	return a
}

// queueOf is ids 1..n at orders 1..n: a queue with nothing to repair.
func queueOf(n int) []*rota.Account {
	list := make([]*rota.Account, 0, n)
	for i := 1; i <= n; i++ {
		list = append(list, acct(i, i, 0))
	}
	return list
}

func ids(list []*rota.Account) []int {
	out := make([]int, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func find(list []*rota.Account, id int) *rota.Account {
	a := rota.FindID(list, id)
	if a == nil {
		panic("no such account in the test list")
	}
	return a
}

func wantIDs(t *testing.T, what string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v want %v", what, got, want)
		}
	}
}

// wantOrders checks every account's stored number, id by id.
func wantOrders(t *testing.T, list []*rota.Account, want map[int]int) {
	t.Helper()
	for _, a := range list {
		if a.Order != want[a.ID] {
			t.Fatalf("account %d at order %d, want %d", a.ID, a.Order, want[a.ID])
		}
	}
}

func wantMoved(t *testing.T, m rotation.Moved, was, now int, shifted []int) {
	t.Helper()
	if m.Was != was || m.Now != now {
		t.Fatalf("was %d now %d, want was %d now %d", m.Was, m.Now, was, now)
	}
	wantIDs(t, "shifted", ids(m.Shifted), shifted)
}

func place(t *testing.T, s string) rotation.Place {
	t.Helper()
	p, err := rotation.ParsePlace(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// openMem seeds a store from raw JSON so any account state is reachable
// without a login.
func openMem(t *testing.T, blob string) (*store.Store, *memBackend) {
	t.Helper()
	b := &memBackend{blob: []byte(blob), home: t.TempDir()}
	st, err := store.NewStore(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, b
}

// plain registers a bare fake so every account in a test names a provider
// that exists, even where nothing looks it up.
func plain(t *testing.T, name string) {
	t.Helper()
	fake.Registry(t)
	rota.Register(fake.New(name))
}

/* ------------------------------------------------------------ predicates -- */

func TestCutoff_RangeAndDefault(t *testing.T) {
	for threshold, want := range map[int]int{0: 100, -1: 100, 101: 100, 70: 70, 1: 1, 100: 100} {
		if got := rotation.Cutoff(&rota.Account{Threshold: threshold}); got != want {
			t.Errorf("threshold %d: cutoff %d, want %d", threshold, got, want)
		}
	}
}

func TestSpent_UsesPercentAgainstCutoff(t *testing.T) {
	at80 := acct(1, 1, 80)
	at80.Threshold = 80
	if !rotation.Spent(at80) {
		t.Fatal("80% against a cutoff of 80 is spent")
	}
	at80.Threshold = 81
	if rotation.Spent(at80) {
		t.Fatal("80% against a cutoff of 81 is not spent")
	}
	at80.Threshold = 0
	if rotation.Spent(at80) {
		t.Fatal("an unset threshold means all of it")
	}
	if !rotation.Spent(acct(2, 1, 100)) {
		t.Fatal("100% against the default cutoff is spent")
	}
	// No reading reports 0, so nothing without a quota is ever spent.
	none := acct(3, 1, -1)
	none.Threshold = 1
	if rotation.Spent(none) {
		t.Fatal("an account with no reading is never spent")
	}
	// A scoped window covers one model, not the account, so it is left out.
	scoped := acct(4, 1, 10)
	scoped.Quota.Windows = append(scoped.Quota.Windows, rota.Window{Name: "m", Percent: 100, Scoped: true})
	scoped.Threshold = 50
	if rotation.Spent(scoped) {
		t.Fatal("a spent scoped window is not a reason to move on")
	}
}

func TestInQueue_OrderAtLeastOne(t *testing.T) {
	for order, want := range map[int]bool{1: true, 7: true, 0: false, -1: false} {
		if got := rotation.InQueue(&rota.Account{Order: order}); got != want {
			t.Errorf("order %d: in queue %v, want %v", order, got, want)
		}
	}
}

func TestAvailable_InQueueNotDeadNotSpent(t *testing.T) {
	if !rotation.Available(acct(1, 1, 0)) {
		t.Fatal("in the queue, alive and unspent is available")
	}
	if rotation.Available(acct(2, 0, 0)) {
		t.Fatal("out of the queue is not available")
	}
	dead := acct(3, 1, 0)
	dead.Dead = true
	if rotation.Available(dead) {
		t.Fatal("dead is not available")
	}
	if rotation.Available(acct(4, 1, 100)) {
		t.Fatal("spent is not available")
	}
}

/* -------------------------------------------------------------- ordering -- */

func TestSort_OrderedFirstThenOrderThenID(t *testing.T) {
	list := []*rota.Account{acct(1, 0, 0), acct(2, 2, 0), acct(3, 0, 0), acct(4, 1, 0), acct(5, 2, 0)}
	rotation.Sort(list)
	wantIDs(t, "sorted", ids(list), []int{4, 2, 5, 1, 3})
}

func TestQueue_FiltersAndReturnsNewSlice(t *testing.T) {
	list := []*rota.Account{acct(1, 0, 0), acct(2, 2, 0), acct(3, 0, 0), acct(4, 1, 0), acct(5, 2, 0)}
	q := rotation.Queue(list)
	wantIDs(t, "queue", ids(q), []int{4, 2, 5})
	q[0], q[1] = q[1], q[0]
	wantIDs(t, "input after mutating the queue", ids(list), []int{1, 2, 3, 4, 5})

	// Even when nothing is filtered out, the input is never handed back.
	all := queueOf(3)
	q = rotation.Queue(all)
	q[0], q[2] = q[2], q[0]
	wantIDs(t, "input after mutating an unfiltered queue", ids(all), []int{1, 2, 3})
}

/* ------------------------------------------------------------------ pick -- */

func TestPick_EmptyQueueMessage(t *testing.T) {
	for _, list := range [][]*rota.Account{nil, {acct(1, 0, 0), acct(2, 0, 0)}} {
		_, err := rotation.Pick(list)
		if !errors.Is(err, rotation.ErrNone) || !strings.Contains(err.Error(), "give one an order") {
			t.Fatalf("got %v", err)
		}
	}
}

func TestPick_ExhaustedMessage(t *testing.T) {
	dead := acct(2, 2, 0)
	dead.Dead = true
	_, err := rotation.Pick([]*rota.Account{acct(1, 1, 100), dead})
	if !errors.Is(err, rotation.ErrNone) || !strings.Contains(err.Error(), "spent or needs re-auth") {
		t.Fatalf("got %v", err)
	}
}

func TestPick_FirstAvailable(t *testing.T) {
	dead := acct(2, 2, 0)
	dead.Dead = true
	// Slice order is not queue order: the pick follows the numbers.
	list := []*rota.Account{acct(4, 4, 0), acct(3, 3, 10), dead, acct(1, 1, 100)}
	got, err := rotation.Pick(list)
	if err != nil || got.ID != 3 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestNext_OnePastHighest(t *testing.T) {
	if got := rotation.Next(nil); got != 1 {
		t.Fatalf("empty list: %d", got)
	}
	if got := rotation.Next([]*rota.Account{acct(1, 0, 0)}); got != 1 {
		t.Fatalf("nothing in the queue: %d", got)
	}
	if got := rotation.Next([]*rota.Account{acct(1, 3, 0), acct(2, 7, 0), acct(3, 0, 0)}); got != 8 {
		t.Fatalf("got %d, want one past 7", got)
	}
}

/* ---------------------------------------------------------------- choose -- */

func TestChoose_ByIDAndNoAccount(t *testing.T) {
	plain(t, "t-plain")
	st, _ := openMem(t, `{"ordered":true,"accounts":[{"id":4,"provider":"t-plain","order":1,"token":{"accessToken":"a"}}]}`)
	got, err := rotation.Choose(context.Background(), st, 4)
	if err != nil || got.ID != 4 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := rotation.Choose(context.Background(), st, 9); !errors.Is(err, rota.ErrNoAccount) {
		t.Fatalf("got %v", err)
	}
}

func TestChoose_RotationRefreshesMeteredThenPicks(t *testing.T) {
	fake.Registry(t)
	var mu sync.Mutex
	calls := map[string]int{}
	rota.Register(fake.Meter{Provider: fake.New("t-m"), Fn: func(_ context.Context, access string) (*rota.Quota, error) {
		mu.Lock()
		calls[access]++
		mu.Unlock()
		pct := 10.0
		if access == "a1" {
			pct = 100
		}
		return &rota.Quota{Windows: []rota.Window{{Name: "5h", Percent: pct, Primary: true}}}, nil
	}})
	st, b := openMem(t, `{"ordered":true,"accounts":[
		{"id":1,"provider":"t-m","order":1,"token":{"accessToken":"a1"}},
		{"id":2,"provider":"t-m","order":2,"token":{"accessToken":"a2"}}]}`)

	got, err := rotation.Choose(context.Background(), st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 2 {
		t.Fatalf("picked %d; only a fresh reading says the first is spent", got.ID)
	}
	if calls["a1"] != 1 || calls["a2"] != 1 {
		t.Fatalf("meter calls %v, want one per account", calls)
	}
	if st.Find(1).Percent() != 100 {
		t.Fatal("the reading taken on the way must be kept")
	}
	if b.saves != 1 {
		t.Fatalf("saves %d, want the readings written once", b.saves)
	}
}

func TestChoose_SkipsBusyAccounts(t *testing.T) {
	fake.Registry(t)
	// An adopter owns its credential file, so a run holds the account.
	rota.Register(fake.Adopter{Provider: fake.New("t-owns"), Fn: func(*rota.Account, string) error { return nil }})
	st, _ := openMem(t, `{"ordered":true,"accounts":[
		{"id":1,"provider":"t-owns","order":1,"token":{"accessToken":"a"}},
		{"id":2,"provider":"t-owns","order":2,"token":{"accessToken":"b"}}]}`)
	ctx := context.Background()

	got, err := rotation.Choose(ctx, st, 0)
	if err != nil || got.ID != 1 {
		t.Fatalf("nothing running, first in the queue: %v %v", got, err)
	}

	release, ok := st.Hold(st.Find(1))
	if !ok {
		t.Fatal("the first hold must succeed")
	}
	t.Cleanup(release)
	got, err = rotation.Choose(ctx, st, 0)
	if err != nil || got.ID != 2 {
		t.Fatalf("a busy account is stepped past: %v %v", got, err)
	}

	release, ok = st.Hold(st.Find(2))
	if !ok {
		t.Fatal("the second hold must succeed")
	}
	t.Cleanup(release)
	_, err = rotation.Choose(ctx, st, 0)
	if !errors.Is(err, rotation.ErrNone) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("all busy must say so, not spent: %v", err)
	}

	// By id the caller asked for that one; Run is where a refusal belongs.
	if got, err := rotation.Choose(ctx, st, 1); err != nil || got.ID != 1 {
		t.Fatalf("an account named by id is not stepped past: %v %v", got, err)
	}
}

/* -------------------------------------------------------------- backfill -- */

func TestBackfill_NumbersByIDOnce(t *testing.T) {
	plain(t, "t-b")
	st, b := openMem(t, `{"accounts":[{"id":3,"provider":"t-b"},{"id":1,"provider":"t-b"},{"id":7,"provider":"t-b"}]}`)
	rotation.Backfill(st)
	wantOrders(t, st.Accounts, map[int]int{1: 1, 3: 2, 7: 3})
	if !st.Ordered {
		t.Fatal("the store must record that it has been ordered")
	}
	if b.saves != 1 {
		t.Fatalf("saves %d, want the numbering written once", b.saves)
	}

	// Once ordered, an account left at 0 is a decision, not an omission.
	st.Find(3).Order = 0
	rotation.Backfill(st)
	wantOrders(t, st.Accounts, map[int]int{1: 1, 3: 0, 7: 3})
	if b.saves != 1 {
		t.Fatalf("saves %d, want no second write", b.saves)
	}
}

func TestBackfill_LeavesOrderedStore(t *testing.T) {
	plain(t, "t-b")
	st, b := openMem(t, `{"ordered":true,"accounts":[{"id":1,"provider":"t-b"},{"id":2,"provider":"t-b"}]}`)
	rotation.Backfill(st)
	wantOrders(t, st.Accounts, map[int]int{1: 0, 2: 0})
	if b.saves != 0 {
		t.Fatalf("saves %d, want none", b.saves)
	}
}

func TestBackfill_SkipsEmptyStore(t *testing.T) {
	for _, blob := range []string{``, `{"accounts":[]}`} {
		st, b := openMem(t, blob)
		rotation.Backfill(st)
		if st.Ordered || b.saves != 0 {
			t.Fatalf("blob %q: ordered %v saves %d; an empty store has nothing to decide", blob, st.Ordered, b.saves)
		}
	}
}

/* ------------------------------------------------------------ parseplace -- */

func TestParsePlace_AllForms(t *testing.T) {
	// Place is opaque, so each form is read back through a Move of account
	// 5 in a queue of eight.
	cases := []struct {
		in  string
		now int
	}{
		{"out", 0}, {"0", 0}, {" Out ", 0},
		{" First ", 1}, {"first", 1},
		{"LAST", 8}, {"last", 8},
		{"up", 4}, {" Down ", 6},
		{"before:3", 3}, {"Before:1", 1},
		{"after:7", 7}, {"AFTER:7", 7},
		{"2", 2}, {" 5 ", 5}, {"8", 8}, {"99", 8},
	}
	for _, c := range cases {
		p, err := rotation.ParsePlace(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		list := queueOf(8)
		m, err := rotation.Move(list, find(list, 5), p)
		if err != nil || m.Now != c.now {
			t.Errorf("%q: now %d err %v, want now %d", c.in, m.Now, err, c.now)
		}
	}
}

func TestParsePlace_Refusals(t *testing.T) {
	for _, in := range []string{"", "-1", "x", "1.5", "before", "before:", "before:x", "before:0", "after:0", "between:2"} {
		if _, err := rotation.ParsePlace(in); !errors.Is(err, rota.ErrInvalidRequest) {
			t.Errorf("%q: got %v, want ErrInvalidRequest", in, err)
		}
	}
}

/* ------------------------------------------------------------------ move -- */

func TestMove_NumberShiftsLaterAccounts(t *testing.T) {
	list := queueOf(5)
	m, err := rotation.Move(list, find(list, 5), place(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 5, 2, []int{2, 3, 4})
	wantOrders(t, list, map[int]int{1: 1, 5: 2, 2: 3, 3: 4, 4: 5})
}

func TestMove_PastEndIsLast(t *testing.T) {
	list := queueOf(4)
	m, err := rotation.Move(list, find(list, 2), place(t, "99"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 2, 4, []int{3, 4})
	wantOrders(t, list, map[int]int{1: 1, 3: 2, 4: 3, 2: 4})
}

func TestMove_OutClosesGap(t *testing.T) {
	list := queueOf(4)
	m, err := rotation.Move(list, find(list, 2), place(t, "out"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 2, 0, []int{3, 4})
	wantOrders(t, list, map[int]int{1: 1, 3: 2, 4: 3, 2: 0})
}

func TestMove_FirstAndLast(t *testing.T) {
	list := queueOf(4)
	m, err := rotation.Move(list, find(list, 3), place(t, "first"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 3, 1, []int{1, 2})
	wantOrders(t, list, map[int]int{3: 1, 1: 2, 2: 3, 4: 4})

	m, err = rotation.Move(list, find(list, 3), place(t, "last"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 1, 4, []int{1, 2, 4})
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 4: 3, 3: 4})

	// An account outside the queue joins at the end and shifts nobody.
	list = append(list, acct(9, 0, 0))
	m, err = rotation.Move(list, find(list, 9), place(t, "last"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 0, 5, nil)
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 4: 3, 3: 4, 9: 5})
}

func TestMove_UpDownTradeWithNeighbour(t *testing.T) {
	list := queueOf(4)
	m, err := rotation.Move(list, find(list, 3), place(t, "up"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 3, 2, []int{2})
	wantOrders(t, list, map[int]int{1: 1, 3: 2, 2: 3, 4: 4})

	m, err = rotation.Move(list, find(list, 3), place(t, "down"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 2, 3, []int{2})
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3, 4: 4})
}

func TestMove_UpAtTopChangesNothing(t *testing.T) {
	list := queueOf(3)
	m, err := rotation.Move(list, find(list, 1), place(t, "up"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 1, 1, nil)
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3})

	// And the mirror: down at the bottom stays at the bottom.
	m, err = rotation.Move(list, find(list, 3), place(t, "down"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 3, 3, nil)
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3})
}

func TestMove_UpDownNeedAPlace(t *testing.T) {
	list := append(queueOf(3), acct(9, 0, 0))
	for _, word := range []string{"up", "down"} {
		m, err := rotation.Move(list, find(list, 9), place(t, word))
		if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), "give it a place first") {
			t.Fatalf("%s: got %v", word, err)
		}
		if m.Was != 0 || m.Now != 0 || len(m.Shifted) != 0 {
			t.Fatalf("%s: on error nothing is reported moved: %+v", word, m)
		}
		wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3, 9: 0})
	}
}

func TestMove_BeforeAfterAnother(t *testing.T) {
	list := queueOf(5)
	m, err := rotation.Move(list, find(list, 1), place(t, "before:4"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 1, 3, []int{2, 3})
	wantOrders(t, list, map[int]int{2: 1, 3: 2, 1: 3, 4: 4, 5: 5})

	m, err = rotation.Move(list, find(list, 1), place(t, "after:5"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 3, 5, []int{4, 5})
	wantOrders(t, list, map[int]int{2: 1, 3: 2, 4: 3, 5: 4, 1: 5})
}

func TestMove_RelativeToSelfOrOutsideIsError(t *testing.T) {
	list := append(queueOf(3), acct(9, 0, 0))
	unchanged := map[int]int{1: 1, 2: 2, 3: 3, 9: 0}
	cases := []struct {
		id   int
		in   string
		want string
	}{
		{2, "before:2", "relative to itself"},
		{9, "after:9", "relative to itself"},
		{2, "after:7", "no account 7 in the rotation"},
		{2, "before:9", "no account 9 in the rotation"}, // exists, but out of the queue
	}
	for _, c := range cases {
		m, err := rotation.Move(list, find(list, c.id), place(t, c.in))
		if !errors.Is(err, rota.ErrInvalidRequest) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%d %s: got %v, want %q", c.id, c.in, err, c.want)
		}
		if m.Was != 0 || m.Now != 0 || len(m.Shifted) != 0 {
			t.Fatalf("%d %s: on error nothing is reported moved: %+v", c.id, c.in, m)
		}
		wantOrders(t, list, unchanged)
	}
}

func TestMove_RepairsTiesAndGaps(t *testing.T) {
	// An old store: two at 3, one at 7, one out. The queue reads 1, 2, 3
	// by id for the tie, and "was" is that position, not the number held.
	list := []*rota.Account{acct(1, 3, 0), acct(2, 3, 0), acct(3, 7, 0), acct(4, 0, 0)}
	m, err := rotation.Move(list, find(list, 2), place(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 2, 2, []int{1, 3})
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3, 4: 0})
}

func TestMove_SamePlaceShiftsNothing(t *testing.T) {
	list := queueOf(3)
	m, err := rotation.Move(list, find(list, 2), place(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, m, 2, 2, nil)
	wantOrders(t, list, map[int]int{1: 1, 2: 2, 3: 3})
}
