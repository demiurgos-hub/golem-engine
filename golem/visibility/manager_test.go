package visibility

import "testing"

func TestPublicDefaultAndJoinLeave(t *testing.T) {
	m := NewManager()
	if !m.Allows(1, 10) {
		t.Fatal("public entity should allow any session")
	}
	m.SetEntityGroup(10, "party")
	if m.Allows(1, 10) {
		t.Fatal("non-member must not see grouped entity")
	}
	m.JoinGroup(1, "party")
	if !m.Allows(1, 10) {
		t.Fatal("member should see grouped entity")
	}
	m.LeaveGroup(1, "party")
	if m.Allows(1, 10) {
		t.Fatal("after leave, session must not see grouped entity")
	}
	if m.InGroup(1, "party") {
		t.Fatal("InGroup should be false after LeaveGroup")
	}
}

func TestGroupReassignmentAndClear(t *testing.T) {
	m := NewManager()
	m.JoinGroup(1, "a")
	m.JoinGroup(2, "b")
	m.SetEntityGroup(10, "a")
	if !m.Allows(1, 10) || m.Allows(2, 10) {
		t.Fatal("entity in group a should be visible only to session 1")
	}
	prev := m.SetEntityGroup(10, "b")
	if prev != "a" {
		t.Fatalf("previous group = %q, want a", prev)
	}
	if m.Allows(1, 10) || !m.Allows(2, 10) {
		t.Fatal("after reassignment to b, only session 2 should see entity")
	}
	prev = m.SetEntityGroup(10, "")
	if prev != "b" {
		t.Fatalf("previous group = %q, want b", prev)
	}
	if !m.Allows(1, 10) || !m.Allows(2, 10) {
		t.Fatal("cleared group means public")
	}
	if m.HasGroupedEntities() {
		t.Fatal("HasGroupedEntities should be false after clear")
	}
}

func TestSessionAndEntityCleanup(t *testing.T) {
	m := NewManager()
	m.JoinGroup(1, "party")
	m.JoinGroup(1, "raid")
	m.SetEntityGroup(10, "party")
	m.SetEntityGroup(11, "raid")
	if !m.HasGroupedEntities() {
		t.Fatal("expected grouped entities")
	}
	m.RemoveSession(1)
	if m.Allows(1, 10) || m.InGroup(1, "raid") {
		t.Fatal("RemoveSession should clear memberships")
	}
	m.RemoveEntity(10)
	m.RemoveEntity(11)
	if m.EntityGroup(10) != "" || m.HasGroupedEntities() {
		t.Fatal("RemoveEntity should clear group assignments")
	}
}

func TestJoinEmptyGroupIgnored(t *testing.T) {
	m := NewManager()
	m.JoinGroup(1, "")
	if m.InGroup(1, "") {
		t.Fatal("empty group join must be ignored")
	}
	if _, ok := m.sessionGroups[1]; ok {
		t.Fatal("empty join must not create session entry")
	}
}

func TestGenerationBumpsOnMutation(t *testing.T) {
	m := NewManager()
	if m.Generation() != 0 {
		t.Fatalf("initial generation = %d, want 0", m.Generation())
	}
	m.SetEntityGroup(1, "a")
	if m.Generation() != 1 {
		t.Fatalf("after SetEntityGroup generation = %d, want 1", m.Generation())
	}
	m.JoinGroup(2, "a")
	if m.Generation() != 2 {
		t.Fatalf("after JoinGroup generation = %d, want 2", m.Generation())
	}
	m.SetEntityGroup(1, "a") // no-op same group
	if m.Generation() != 2 {
		t.Fatalf("noop SetEntityGroup bumped generation to %d", m.Generation())
	}
}
