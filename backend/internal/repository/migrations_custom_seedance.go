package repository

import "strings"

const openCodePlatformMigration = "238_opencode_go_platform.sql"

// These are the unchanged upstream migration's SHA256 values after TrimSpace,
// for LF and Windows CRLF checkouts respectively. They do not relax checksum
// validation: the runner still validates and records the original file content.
const (
	openCodePlatformMigrationChecksumLF   = "6f987e251519bd3759e60da44620a5d777494cceb333b6ce394aa0ea536ef5a2"
	openCodePlatformMigrationChecksumCRLF = "10b12b7a31255bc269f35fedbb9609b68097c71707cf5a229de0c465fb0b6fcc"
)

// customSeedanceMigrationExecutionSQL preserves existing Seedance quota rows
// while migration 238 adds OpenCode. A later migration alone cannot repair this
// upgrade path: the original CHECK would reject those rows and abort startup.
// Migration 239 also restores the platform for databases that already ran 238.
// Only this exact upstream file and its quota CHECK are changed for execution;
// composite routes and monitor provider constraints retain upstream behavior.
func customSeedanceMigrationExecutionSQL(name, checksum, content string) string {
	if name != openCodePlatformMigration ||
		(checksum != openCodePlatformMigrationChecksumLF && checksum != openCodePlatformMigrationChecksumCRLF) {
		return content
	}

	oldCheck := "CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',\n" +
		"                        'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'))"
	newCheck := strings.Replace(oldCheck, "'kimi'", "'seedance', 'kimi'", 1)
	if checksum == openCodePlatformMigrationChecksumCRLF {
		oldCheck = strings.ReplaceAll(oldCheck, "\n", "\r\n")
		newCheck = strings.ReplaceAll(newCheck, "\n", "\r\n")
	}
	return strings.Replace(content, oldCheck, newCheck, 1)
}
