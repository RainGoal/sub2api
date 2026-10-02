package repository

import "strings"

const openCodePlatformMigration = "238_opencode_go_platform.sql"
const typeSafePlatformMigration = "241_add_typesafe_platform.sql"

// These are the unchanged upstream migration's SHA256 values after TrimSpace,
// for LF and Windows CRLF checkouts respectively. They do not relax checksum
// validation: the runner still validates and records the original file content.
const (
	openCodePlatformMigrationChecksumLF   = "6f987e251519bd3759e60da44620a5d777494cceb333b6ce394aa0ea536ef5a2"
	openCodePlatformMigrationChecksumCRLF = "10b12b7a31255bc269f35fedbb9609b68097c71707cf5a229de0c465fb0b6fcc"
	typeSafePlatformMigrationChecksumLF   = "b6559525bf8d0b5d7c617e7415944f0d1fae8c408e9131ace84139795f66dcd2"
	typeSafePlatformMigrationChecksumCRLF = "5dfc588dbb9ceaaf88be304be8f3998639223a1d9d3e3629c0588a0fbc8e3b54"
)

// customSeedanceMigrationExecutionSQL preserves existing Seedance quota rows
// while migrations 238 and 241 add OpenCode and TypeSafe. A later migration alone cannot repair this
// upgrade path: the original CHECK would reject those rows and abort startup.
// Migrations 239 and 242 also restore it for databases that already ran the upstream SQL.
// Only these exact upstream files and their quota CHECK are changed for execution;
// composite routes and monitor provider constraints retain upstream behavior.
func customSeedanceMigrationExecutionSQL(name, checksum, content string) string {
	var checksumLF, checksumCRLF, extraPlatforms string
	switch name {
	case openCodePlatformMigration:
		checksumLF, checksumCRLF = openCodePlatformMigrationChecksumLF, openCodePlatformMigrationChecksumCRLF
	case typeSafePlatformMigration:
		checksumLF, checksumCRLF = typeSafePlatformMigrationChecksumLF, typeSafePlatformMigrationChecksumCRLF
		extraPlatforms = ", 'typesafe'"
	default:
		return content
	}
	if checksum != checksumLF && checksum != checksumCRLF {
		return content
	}

	oldCheck := "CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',\n" +
		"                        'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'" + extraPlatforms + "))"
	newCheck := strings.Replace(oldCheck, "'kimi'", "'seedance', 'kimi'", 1)
	if checksum == checksumCRLF {
		oldCheck = strings.ReplaceAll(oldCheck, "\n", "\r\n")
		newCheck = strings.ReplaceAll(newCheck, "\n", "\r\n")
	}
	return strings.Replace(content, oldCheck, newCheck, 1)
}
