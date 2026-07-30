package slackapp

import "testing"

func TestClassifyChatPrivate(t *testing.T) {
	chatType, isPrivate := classifyChat("D123", "im")
	if chatType != "private" || !isPrivate {
		t.Fatalf("expected private/true, got %q/%v", chatType, isPrivate)
	}
}

func TestClassifyChatPrivateByPrefix(t *testing.T) {
	chatType, isPrivate := classifyChat("D999", "")
	if chatType != "private" || !isPrivate {
		t.Fatalf("expected private/true for D-prefix, got %q/%v", chatType, isPrivate)
	}
}

func TestClassifyChatPublic(t *testing.T) {
	chatType, isPrivate := classifyChat("C123", "channel")
	if chatType != "public" || isPrivate {
		t.Fatalf("expected public/false, got %q/%v", chatType, isPrivate)
	}
}

func TestTruncateLogEmpty(t *testing.T) {
	if got := truncateLog("", 100); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestTruncateLogShort(t *testing.T) {
	if got := truncateLog("hello", 100); got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}

func TestTruncateLogTruncates(t *testing.T) {
	got := truncateLog("abcdef", 3)
	if got != "abc…" {
		t.Fatalf("expected 'abc…', got %q", got)
	}
}

func TestTruncateLogTrims(t *testing.T) {
	if got := truncateLog("  padded  ", 100); got != "padded" {
		t.Fatalf("expected trimmed, got %q", got)
	}
}

func TestTruncateLogZeroMax(t *testing.T) {
	if got := truncateLog("hello", 0); got != "hello" {
		t.Fatalf("expected no truncation with max=0, got %q", got)
	}
}

func TestNormalizeSlackMarkdownEmpty(t *testing.T) {
	if got := normalizeSlackMarkdown(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestNormalizeSlackMarkdownPlainText(t *testing.T) {
	input := "Hello world, no special chars"
	if got := normalizeSlackMarkdown(input); got != input {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestNormalizeSlackMarkdownBold(t *testing.T) {
	got := normalizeSlackMarkdown("This is **bold** text")
	if !containsStr(got, "*bold*") {
		t.Fatalf("expected Slack bold, got %q", got)
	}
	if containsStr(got, "**") {
		t.Fatalf("expected no markdown bold markers, got %q", got)
	}
}

func TestNormalizeSlackMarkdownItalic(t *testing.T) {
	got := normalizeSlackMarkdown("This is *italic* text")
	if !containsStr(got, "_italic_") {
		t.Fatalf("expected Slack italic, got %q", got)
	}
}

func TestNormalizeSlackMarkdownLink(t *testing.T) {
	got := normalizeSlackMarkdown("[Click here](https://example.com)")
	if !containsStr(got, "<https://example.com|Click here>") {
		t.Fatalf("expected Slack link format, got %q", got)
	}
}

func TestNormalizeSlackMarkdownHeading(t *testing.T) {
	got := normalizeSlackMarkdown("## Heading")
	if !containsStr(got, "*Heading*") {
		t.Fatalf("expected Slack bold for heading, got %q", got)
	}
}

func TestNormalizeSlackMarkdownCodeBlockUnchanged(t *testing.T) {
	input := "```\n**bold inside code**\n```"
	got := normalizeSlackMarkdown(input)
	if !containsStr(got, "**bold inside code**") {
		t.Fatalf("expected code block content unchanged, got %q", got)
	}
}

func TestNormalizeSlackMarkdownInlineCodeUnchanged(t *testing.T) {
	input := "Use `**bold**` in code"
	got := normalizeSlackMarkdown(input)
	if !containsStr(got, "`**bold**`") {
		t.Fatalf("expected inline code content unchanged, got %q", got)
	}
}

func TestConvertMarkdownLinkValid(t *testing.T) {
	converted, advance := convertMarkdownLink("[text](https://url.com)")
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "<https://url.com|text>" {
		t.Fatalf("expected Slack link, got %q", converted)
	}
}

func TestConvertMarkdownLinkEmptyLabel(t *testing.T) {
	_, advance := convertMarkdownLink("[](https://url.com)")
	if advance != 0 {
		t.Fatal("expected zero advance for empty label")
	}
}

func TestConvertMarkdownLinkNoURL(t *testing.T) {
	_, advance := convertMarkdownLink("[text]()")
	if advance != 0 {
		t.Fatal("expected zero advance for empty URL")
	}
}

func TestConvertMarkdownHeadingH1(t *testing.T) {
	converted, advance := convertMarkdownHeading("# Title")
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "*Title*" {
		t.Fatalf("expected '*Title*', got %q", converted)
	}
}

func TestConvertMarkdownHeadingH3(t *testing.T) {
	converted, advance := convertMarkdownHeading("### Deep heading")
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "*Deep heading*" {
		t.Fatalf("expected '*Deep heading*', got %q", converted)
	}
}

func TestConvertMarkdownHeadingNoSpace(t *testing.T) {
	_, advance := convertMarkdownHeading("#NoSpace")
	if advance != 0 {
		t.Fatal("expected zero advance for missing space after #")
	}
}

func TestConvertMarkdownBoldDoubleAsterisk(t *testing.T) {
	converted, advance := convertMarkdownBold("**bold text** rest", "**")
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "*bold text*" {
		t.Fatalf("expected '*bold text*', got %q", converted)
	}
}

func TestConvertMarkdownBoldDoubleUnderscore(t *testing.T) {
	converted, advance := convertMarkdownBold("__bold text__ rest", "__")
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "*bold text*" {
		t.Fatalf("expected '*bold text*', got %q", converted)
	}
}

func TestConvertMarkdownBoldUnmatched(t *testing.T) {
	_, advance := convertMarkdownBold("**no close", "**")
	if advance != 0 {
		t.Fatal("expected zero advance for unmatched bold")
	}
}

func TestConvertMarkdownItalicSingleAsterisk(t *testing.T) {
	text := "This *italic* here"
	converted, advance := convertMarkdownItalic(text, 5)
	if advance == 0 {
		t.Fatal("expected non-zero advance")
	}
	if converted != "_italic_" {
		t.Fatalf("expected '_italic_', got %q", converted)
	}
}

func TestConvertMarkdownItalicUnmatched(t *testing.T) {
	text := "This *no close"
	_, advance := convertMarkdownItalic(text, 5)
	if advance != 0 {
		t.Fatal("expected zero advance for unmatched italic")
	}
}

func TestLooksLikeTableSeparatorValid(t *testing.T) {
	cases := []string{
		"|---|---|",
		"| --- | --- |",
		"|:---:|---:|",
	}
	for _, input := range cases {
		if !looksLikeTableSeparator(input) {
			t.Errorf("expected %q to look like separator", input)
		}
	}
}

func TestLooksLikeTableSeparatorInvalid(t *testing.T) {
	cases := []string{
		"",
		"no pipes",
		"|abc|def|",
	}
	for _, input := range cases {
		if looksLikeTableSeparator(input) {
			t.Errorf("expected %q to NOT look like separator", input)
		}
	}
}

func TestSplitTableRow(t *testing.T) {
	got := splitTableRow("| Name | Value |")
	if len(got) != 2 || got[0] != "Name" || got[1] != "Value" {
		t.Fatalf("expected [Name, Value], got %v", got)
	}
}

func TestFormatTableHeaderTwoCells(t *testing.T) {
	got := formatTableHeader([]string{"Key", "Value"})
	if got != "*Key* - *Value*" {
		t.Fatalf("expected '*Key* - *Value*', got %q", got)
	}
}

func TestFormatTableHeaderSingleCell(t *testing.T) {
	got := formatTableHeader([]string{"Only"})
	if got != "" {
		t.Fatalf("expected empty for single cell, got %q", got)
	}
}

func TestFormatTableRowTwoCells(t *testing.T) {
	got := formatTableRow([]string{"foo", "bar"})
	if got != "foo - bar" {
		t.Fatalf("expected 'foo - bar', got %q", got)
	}
}

func TestConvertMarkdownTablesBasic(t *testing.T) {
	input := "| Name | Age |\n| --- | --- |\n| Alice | 30 |"
	got := convertMarkdownTables(input)
	if containsStr(got, "| --- |") {
		t.Fatalf("expected separator removed, got %q", got)
	}
	if !containsStr(got, "Alice") {
		t.Fatalf("expected row data preserved, got %q", got)
	}
}

func TestConvertMarkdownTablesEmpty(t *testing.T) {
	if got := convertMarkdownTables(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// helper to avoid importing strings in this test file for simple checks
func containsStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
