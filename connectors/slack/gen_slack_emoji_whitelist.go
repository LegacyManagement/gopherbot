//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type emojiEntry struct {
	ShortName  string   `json:"short_name"`
	ShortNames []string `json:"short_names"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: go run ./connectors/slack/gen_slack_emoji_whitelist.go <iamcal emoji.json> <output.go>\n")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	var entries []emojiEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		panic(err)
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		addShortcode(seen, entry.ShortName)
		for _, name := range entry.ShortNames {
			addShortcode(seen, name)
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	var out strings.Builder
	out.WriteString("package slack\n\n")
	out.WriteString("// Code generated from iamcal/emoji-data emoji.json; DO NOT EDIT.\n")
	out.WriteString("// Source: https://github.com/iamcal/emoji-data/blob/master/emoji.json\n")
	out.WriteString("var slackSupportedEmojiShortcodes = map[string]struct{}{\n")
	for _, name := range names {
		fmt.Fprintf(&out, "\t%q: {},\n", name)
	}
	out.WriteString("}\n")

	if err := os.WriteFile(os.Args[2], []byte(out.String()), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d Slack emoji shortcodes to %s\n", len(names), os.Args[2])
}

func addShortcode(seen map[string]struct{}, shortcode string) {
	shortcode = strings.TrimSpace(shortcode)
	if shortcode == "" {
		return
	}
	seen[shortcode] = struct{}{}
}
