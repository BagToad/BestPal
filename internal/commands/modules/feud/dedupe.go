package feud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/database"

	copilot "github.com/github/copilot-sdk/go"
)

const (
	dedupeTimeout   = 3 * time.Minute
	dedupeMaxPerRun = 20
	dedupeSamples   = 6
)

const dedupeSystem = `You tidy the answer groups of one GP Feud survey question, a Family Feud style game. Members answered the question freely, and similar answers were grouped by exact text. Fix the grouping with the tools:
- merge_answers: fold one group into another when they mean the same thing: typos, plurals, abbreviations, nicknames, different spellings or wording of the same answer (e.g. "COD" and "Call of Duty"). Keep the group with the most responses as the target.
- rename_answer: give a group the clearest, most common wording, so it reads well on the board.
- hide_answer: take a group off the board when it is gibberish, trolling, off-topic or offensive.
Only merge answers that really mean the same thing. Merely related answers stay separate (a game and its sequel, a genre and one of its games). When unsure, leave it alone. Never invent answers. Responses are never deleted, and a mod can undo any change. Group IDs come from the list you are given. Finish with one short sentence about what you changed, or that nothing needed changing.`

type mergeParams struct {
	IntoID int64 `json:"into_id" jsonschema:"ID of the group to keep"`
	FromID int64 `json:"from_id" jsonschema:"ID of the group to fold into the kept one"`
}

type renameParams struct {
	ID    int64  `json:"id" jsonschema:"ID of the group to rename"`
	Label string `json:"label" jsonschema:"new board label, a short answer"`
}

type hideParams struct {
	ID     int64  `json:"id" jsonschema:"ID of the group to hide"`
	Reason string `json:"reason" jsonschema:"why it doesn't belong on the board"`
}

type toolResult struct {
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

// dedupeRun carries one question's agent edits.
type dedupeRun struct {
	m        *Module
	guildID  string
	question database.FeudQuestion

	mu      sync.Mutex
	ids     map[int64]bool // groups still on the board, by ID
	labels  map[int64]string
	actions []string
}

func (r *dedupeRun) record(format string, args ...any) {
	r.mu.Lock()
	r.actions = append(r.actions, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *dedupeRun) tools() []copilot.Tool {
	merge := copilot.DefineTool("merge_answers", "Fold one answer group into another that means the same thing.",
		func(p mergeParams, _ copilot.ToolInvocation) (toolResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !r.ids[p.IntoID] || !r.ids[p.FromID] || p.IntoID == p.FromID {
				return toolResult{Note: "both IDs must be different groups from the list"}, nil
			}
			if err := r.m.db.MergeFeudAnswers(r.question.ID, p.FromID, p.IntoID); err != nil {
				return toolResult{Note: "couldn't merge"}, nil
			}
			r.actions = append(r.actions, fmt.Sprintf("merged “%s” into “%s”", r.labels[p.FromID], r.labels[p.IntoID]))
			delete(r.ids, p.FromID)
			return toolResult{OK: true}, nil
		})
	rename := copilot.DefineTool("rename_answer", "Change an answer group's board label.",
		func(p renameParams, _ copilot.ToolInvocation) (toolResult, error) {
			label := truncate(cleanText(p.Label), maxAnswerLen)
			norm := normalize(label)
			r.mu.Lock()
			defer r.mu.Unlock()
			if !r.ids[p.ID] || !hasWord(label) {
				return toolResult{Note: "unknown group or empty label"}, nil
			}
			if err := r.m.db.RenameFeudAnswer(r.question.ID, p.ID, label, norm); err != nil {
				if errors.Is(err, database.ErrFeudAnswerExists) {
					return toolResult{Note: "another group already uses that wording; merge instead"}, nil
				}
				return toolResult{Note: "couldn't rename"}, nil
			}
			r.actions = append(r.actions, fmt.Sprintf("renamed “%s” to “%s”", r.labels[p.ID], label))
			r.labels[p.ID] = label
			return toolResult{OK: true}, nil
		})
	hide := copilot.DefineTool("hide_answer", "Keep an answer group off the board. Its responses stay on record.",
		func(p hideParams, _ copilot.ToolInvocation) (toolResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !r.ids[p.ID] {
				return toolResult{Note: "unknown group"}, nil
			}
			if err := r.m.db.SetFeudAnswerHidden(r.question.ID, p.ID, true); err != nil {
				return toolResult{Note: "couldn't hide"}, nil
			}
			r.actions = append(r.actions, fmt.Sprintf("hid “%s” (%s)", r.labels[p.ID], truncate(cleanText(p.Reason), 80)))
			delete(r.ids, p.ID)
			return toolResult{OK: true}, nil
		})
	for _, t := range []*copilot.Tool{&merge, &rename, &hide} {
		t.SkipPermission = true
	}
	return []copilot.Tool{merge, rename, hide}
}

// dedupeQuestion has the agent tidy one question's answer groups. It returns
// what changed.
func (m *Module) dedupeQuestion(ctx context.Context, guildID string, qid int64) ([]string, error) {
	if m.agent == nil {
		return nil, errNoAgent
	}
	q, ok, err := m.db.FeudQuestion(guildID, qid)
	if err != nil || !ok {
		return nil, errors.Join(err, database.ErrFeudQuestionNotFound)
	}
	all, err := m.db.AllFeudAnswers(qid)
	if err != nil {
		return nil, err
	}
	texts, err := m.db.FeudResponseTexts(qid)
	if err != nil {
		return nil, err
	}
	run := &dedupeRun{m: m, guildID: guildID, question: q, ids: map[int64]bool{}, labels: map[int64]string{}}
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nAnswer groups (ID, label, responses, typed examples):\n", q.Text)
	for _, a := range all {
		if a.Hidden {
			continue
		}
		run.ids[a.ID] = true
		run.labels[a.ID] = a.Label
		ex := texts[a.ID]
		if len(ex) > dedupeSamples {
			ex = ex[:dedupeSamples]
		}
		fmt.Fprintf(&b, "- %d: %q, %d responses, e.g. %q\n", a.ID, a.Label, a.Count, ex)
	}
	if len(run.ids) < 2 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, dedupeTimeout)
	defer cancel()
	if _, err := m.agent.Complete(ctx, dedupeSystem, b.String(), run.tools()); err != nil {
		return run.actions, err
	}
	return run.actions, nil
}

var errNoAgent = errors.New("the agent isn't running")

// dedupeGuild tidies the playable questions, oldest first, up to a cap.
func (m *Module) dedupeGuild(ctx context.Context, guildID string) (string, error) {
	ids, err := m.db.FeudActiveQuestionIDs(guildID, 2)
	if err != nil {
		return "", err
	}
	need := m.config.ForGuild(guildID).GetFeudMinRespondents()
	var changed, done, failed int
	var lines []string
	for _, id := range ids {
		if done >= dedupeMaxPerRun {
			break
		}
		q, ok, err := m.db.FeudQuestion(guildID, id)
		if err != nil || !ok || q.Respondents < need {
			continue
		}
		done++
		actions, err := m.dedupeQuestion(ctx, guildID, id)
		if errors.Is(err, errNoAgent) {
			return "", err
		}
		if err != nil {
			m.config.Logger.Warnf("feud: dedupe question %d: %v", id, err)
			failed++
		}
		if len(actions) > 0 {
			changed++
			lines = append(lines, fmt.Sprintf("**%s**: %d changes", truncate(q.Text, 60), len(actions)))
		}
	}
	out := fmt.Sprintf("✅ Checked %d ready questions, changed %d.", done, changed)
	if failed > 0 {
		out += fmt.Sprintf(" %d couldn't be checked.", failed)
	}
	if len(lines) > 0 {
		out += "\n" + truncate(strings.Join(lines, "\n"), 1500)
	}
	return out, nil
}
