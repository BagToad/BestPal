package trickortreat

import (
	"fmt"

	"gamerpal/internal/database"
)

// Souvenir types stored in the database.
const (
	souvenirEggshell       = "eggshell"
	souvenirTPRoll         = "tp_roll"
	souvenirToothbrush     = "toothbrush"
	souvenirTornFabric     = "torn_fabric"
	souvenirGhostlyWrapper = "ghostly_wrapper"
)

type souvenirInfo struct {
	emoji, name string
}

var souvenirs = map[string]souvenirInfo{
	souvenirEggshell:       {"🥚", "Eggshell"},
	souvenirTPRoll:         {"🧻", "TP Roll"},
	souvenirToothbrush:     {"🪥", "Toothbrush"},
	souvenirTornFabric:     {"🧵", "Torn Fabric"},
	souvenirGhostlyWrapper: {"👻", "Ghostly Wrapper"},
}

// Buff sources shown in /bucket.
const (
	bonusSourceDingDong  = "Ding Dong Ditch"
	bonusSourceFullSized = "Full-Sized Bar House"
)

// trickResult is what a trick outcome did, for the public reply and the log.
type trickResult struct {
	// Flavor is the story line.
	Flavor string
	// Effects are the parts of the "Outcome:" line; empty means no change.
	Effects []string
	// Log is the short action log text after "triggered a TRICK: ".
	Log string
	// Target is another user the trick involved, pinged in the reply.
	Target string
}

// trickOutcome is one row of the 20-outcome TRICK table.
type trickOutcome struct {
	ID   int
	Name string
	Run  func(tx database.TrickTx, userID string, randN func(int) int) (trickResult, error)
}

func candies(n int64) string {
	if n == 1 || n == -1 {
		return "1 candy"
	}
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%d candies", n)
}

func candyEffect(delta int64, who string) string {
	word := "Candies"
	if delta == 1 || delta == -1 {
		word = "Candy"
	}
	if delta > 0 {
		return fmt.Sprintf("+%d %s to %s", delta, word, who)
	}
	return fmt.Sprintf("%d %s from %s", delta, word, who)
}

// lose takes up to n candies from the user, with flavor text for losing some
// and for having none to lose.
func lose(n int64, flavor func(lost int64) string, emptyFlavor, log string) func(database.TrickTx, string, func(int) int) (trickResult, error) {
	return func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		lost, err := tx.AddCandies(userID, -n)
		if err != nil {
			return trickResult{}, err
		}
		if lost == 0 {
			return trickResult{Flavor: emptyFlavor, Log: log}, nil
		}
		return trickResult{Flavor: flavor(-lost), Effects: []string{candyEffect(lost, mention(userID))}, Log: log}, nil
	}
}

func souvenir(itemType, flavor, log string) func(database.TrickTx, string, func(int) int) (trickResult, error) {
	return func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		if err := tx.AddSouvenir(userID, itemType); err != nil {
			return trickResult{}, err
		}
		s := souvenirs[itemType]
		return trickResult{Flavor: flavor, Effects: []string{fmt.Sprintf("+1 %s %s souvenir in your /bucket", s.emoji, s.name)}, Log: log}, nil
	}
}

func bonus(n int, source, flavor, log string) func(database.TrickTx, string, func(int) int) (trickResult, error) {
	return func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		if err := tx.GrantBonus(userID, n, source); err != nil {
			return trickResult{}, err
		}
		word := "Candies"
		if n == 1 {
			word = "Candy"
		}
		return trickResult{Flavor: flavor, Effects: []string{fmt.Sprintf("+%d Bonus %s on your next claim", n, word)}, Log: log}, nil
	}
}

func timeout(n int, source, flavor, log string) func(database.TrickTx, string, func(int) int) (trickResult, error) {
	return func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		if err := tx.GrantTimeout(userID, n, source); err != nil {
			return trickResult{}, err
		}
		return trickResult{Flavor: flavor, Effects: []string{"Timed out for the next " + spawns(n)}, Log: log}, nil
	}
}

func spawns(n int) string {
	if n == 1 {
		return "candy bowl"
	}
	return fmt.Sprintf("%d candy bowls", n)
}

// giveAway moves one candy from the user to target, if they have one.
func giveAway(tx database.TrickTx, userID, target string) (bool, error) {
	u, err := tx.User(userID)
	if err != nil || u.Candies < 1 {
		return false, err
	}
	if _, err := tx.AddCandies(userID, -1); err != nil {
		return false, err
	}
	if _, err := tx.AddCandies(target, 1); err != nil {
		return false, err
	}
	return true, nil
}

var trickOutcomes = []trickOutcome{
	{1, "Clumsy TP Escape", lose(10,
		func(lost int64) string {
			return fmt.Sprintf("You tripped over a TP roll while sprinting away! You lost %s into the dark street.", candies(lost))
		},
		"You tripped over a TP roll while sprinting away! Good thing your bucket was already empty.",
		"Tripped over a TP roll! 🧻")},
	{2, "Big Sibling Tax", lose(1,
		func(int64) string {
			return "A big sibling stepped in to \"inspect your bag\" and ate 1 candy right in front of you!"
		},
		"A big sibling stepped in to \"inspect your bag\", but it was empty. They wandered off disappointed.",
		"Paid the big sibling tax! 🍫")},
	{3, "Jump-Scare in Bushes", lose(1,
		func(int64) string {
			return "Someone in a scary mask jumped out from behind a pumpkin! You dropped 1 candy into a storm drain."
		},
		"Someone in a scary mask jumped out from behind a pumpkin! Luckily you had no candy to drop.",
		"Jump-scared in the bushes! 😱")},
	{4, "Sharing is Caring", func(tx database.TrickTx, userID string, randN func(int) int) (trickResult, error) {
		const log = "Shared a candy! 🍬"
		targets, err := tx.EmptyHandedParticipants(userID)
		if err != nil {
			return trickResult{}, err
		}
		if len(targets) == 0 {
			return trickResult{Flavor: "You looked around for someone with an empty bag to share with, but everyone already got candy from this bowl.", Log: log}, nil
		}
		target := targets[randN(len(targets))]
		gave, err := giveAway(tx, userID, target)
		if err != nil {
			return trickResult{}, err
		}
		if !gave {
			return trickResult{Flavor: fmt.Sprintf("You spotted %s standing nearby with an empty bag, but your bucket is empty too!", mention(target)), Log: log, Target: target}, nil
		}
		return trickResult{
			Flavor:  fmt.Sprintf("You spotted %s standing nearby with an empty bag and kindly handed them 1 candy!", mention(target)),
			Effects: []string{candyEffect(-1, mention(userID)), candyEffect(1, mention(target))},
			Log:     log, Target: target,
		}, nil
	}},
	{5, "Compassionate Ghost", func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		const log = "A ghost took a candy for charity! 👻"
		target, ok, err := tx.LowestEarner(userID)
		if err != nil {
			return trickResult{}, err
		}
		if !ok {
			return trickResult{Flavor: "A spooky ghost floated by looking for someone in need, but found nobody and drifted away.", Log: log}, nil
		}
		gave, err := giveAway(tx, userID, target)
		if err != nil {
			return trickResult{}, err
		}
		if !gave {
			return trickResult{Flavor: "A spooky ghost floated by to take 1 candy from your bucket, but found it empty and drifted away.", Log: log}, nil
		}
		return trickResult{
			Flavor:  fmt.Sprintf("A spooky ghost floated by, took 1 candy from your bucket, and delivered it directly to %s!", mention(target)),
			Effects: []string{candyEffect(-1, mention(userID)), candyEffect(1, mention(target))},
			Log:     log, Target: target,
		}, nil
	}},
	{6, "Egging Front Porch", souvenir(souvenirEggshell,
		"Out of candy?! You egged the front porch in retaliation! 🥚", "Egged the front porch! 🥚")},
	{7, "Ding Dong Ditch", bonus(1, bonusSourceDingDong,
		"You rang the doorbell and ran! Adrenaline rush: +1 Bonus Candy on your next claim.", "Ding dong ditched! 🔔")},
	{8, "TP House Trip", souvenir(souvenirTPRoll,
		"You toilet-papered the front trees because the bowl was empty! 🧻", "TP'd the front trees! 🧻")},
	{9, "Shoelaces Untied", timeout(1, "Shoelaces Untied",
		"You tripped over your laces! Timed out for the next 1 candy bowl spawn.", "Tripped over their laces! 👟")},
	{10, "Barking Guard Dog", timeout(2, "Barking Guard Dog",
		"A dog in a hot dog costume barked you off the porch! Timed out for the next 2 spawns.", "Barked off the porch! 🐕")},
	{11, "Sidewalk Sugar Crash", timeout(2, "Sidewalk Sugar Crash",
		"You ate too much candy on the sidewalk! Timed out for the next 2 spawns.", "Sugar crashed on the sidewalk! 🍭")},
	{12, "Parent Safety Check", timeout(2, "Parent Safety Check",
		"Your parents pulled you aside for a bag check! Timed out for the next 2 spawns.", "Got a parent bag check! 🔦")},
	{13, "Flashlight Battery", timeout(1, "Flashlight Battery",
		"Your flashlight died in an alley! Timed out for the next 1 spawn.", "Flashlight died! 🔦")},
	{14, "Full-Sized Bar", bonus(3, bonusSourceFullSized,
		"You hit the legendary house! +3 Bonus Candies on your next successful claim.", "Found the full-sized bar house! 🍫")},
	{15, "Alleyway Shortcut", func(tx database.TrickTx, userID string, randN func(int) int) (trickResult, error) {
		const log = "Took the alleyway shortcut! 🌙"
		if randN(2) == 0 {
			if _, err := tx.AddCandies(userID, 2); err != nil {
				return trickResult{}, err
			}
			return trickResult{Flavor: "You took a dark alley shortcut and found 2 candies along the way!", Effects: []string{candyEffect(2, mention(userID))}, Log: log}, nil
		}
		lost, err := tx.AddCandies(userID, -1)
		if err != nil {
			return trickResult{}, err
		}
		if lost == 0 {
			return trickResult{Flavor: "You took a dark alley shortcut and tripped, but had no candy to drop.", Log: log}, nil
		}
		return trickResult{Flavor: "You took a dark alley shortcut and dropped 1 candy into the dark.", Effects: []string{candyEffect(-1, mention(userID))}, Log: log}, nil
	}},
	{16, "Porch Pumpkin Stash", func(tx database.TrickTx, userID string, _ func(int) int) (trickResult, error) {
		if _, err := tx.AddCandies(userID, 2); err != nil {
			return trickResult{}, err
		}
		return trickResult{
			Flavor:  "You inspected a glowing pumpkin and found a hidden stash of 2 candies! 🎃",
			Effects: []string{candyEffect(2, mention(userID))},
			Log:     "Found a pumpkin stash! 🎃",
		}, nil
	}},
	{17, "Animatronic Scare", lose(1,
		func(int64) string {
			return "An animatronic skeleton jumped out and roared! You dropped 1 candy onto the lawn."
		},
		"An animatronic skeleton jumped out and roared! Luckily you had no candy to drop.",
		"Scared by an animatronic skeleton! 💀")},
	{18, "Dentist's House", souvenir(souvenirToothbrush,
		"The local dentist handed you hygiene supplies instead of sweets! 🪥", "Got a toothbrush from the dentist! 🪥")},
	{19, "Costume Tear", souvenir(souvenirTornFabric,
		"You snagged your costume on a thorny bush! 🧵", "Tore their costume! 🧵")},
	{20, "Empty Wrapper", souvenir(souvenirGhostlyWrapper,
		"Someone snuck an empty wrapper into your bucket! 👻", "Got an empty wrapper! 👻")},
}

func mention(userID string) string { return "<@" + userID + ">" }
