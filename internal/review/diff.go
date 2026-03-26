package review

import (
	"strconv"
	"strings"
)

// maxDiffSize 是 ParseDiff 接受的最大输入大小（10MB）。
// 超大 diff 经 strings.Split 后内存放大数倍，限制输入以防 OOM。
const maxDiffSize = 10 * 1024 * 1024

// DiffMap 存储 PR diff 中每个文件的行号映射信息
type DiffMap struct {
	files map[string]*FileDiff
}

// FileDiff 单个文件的 diff 信息
type FileDiff struct {
	NewName  string      // diff 后的文件名（rename 场景）
	IsDelete bool        // 是否为删除文件（+++ /dev/null）
	Hunks    []HunkRange // 所有 hunk 的行号范围
}

// HunkRange 一个 hunk 块的信息
type HunkRange struct {
	NewStart   int        // 新文件起始行号（@@ 中的 +c）
	NewCount   int        // 新文件行数（@@ 中的 d）
	DiffOffset int        // Reserved for Semantic B：该 hunk 在文件 diff 块中的起始行偏移
	Lines      []DiffLine // Reserved for Semantic B：hunk 内逐行解析结果
}

// DiffLine hunk 内单行的解析结果。
// Reserved for Semantic B：当前语义 A 不填充此结构，仅保留类型定义供未来使用。
type DiffLine struct {
	NewLineNum int  // 新文件中的行号（- 行为 0）
	DiffPos    int  // 该行在文件 diff 块中的位置（从 1 开始）
	Op         byte // '+', '-', ' '（context）
}

// ParseDiff 解析标准 unified diff 格式，返回 DiffMap。
// 支持多文件、rename、新增文件（/dev/null -> b/path）、删除文件（a/path -> /dev/null）。
// 二进制文件 diff 跳过处理，不影响其他文件。
func ParseDiff(diffText string) *DiffMap {
	dm := &DiffMap{files: make(map[string]*FileDiff)}
	if len(diffText) == 0 || len(diffText) > maxDiffSize {
		return dm
	}
	if strings.TrimSpace(diffText) == "" {
		return dm
	}

	// 按 "diff --git" 分割为文件块（第一个空块忽略）
	chunks := strings.Split(diffText, "\ndiff --git ")
	for i, chunk := range chunks {
		// 第一个 chunk 可能以 "diff --git " 开头（无前导换行），也可能为空
		if i == 0 {
			// 去掉可能存在的前缀
			chunk = strings.TrimPrefix(chunk, "diff --git ")
		}
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		parseFileChunk(dm, chunk)
	}
	return dm
}

// parseFileChunk 解析单个文件的 diff 块
func parseFileChunk(dm *DiffMap, chunk string) {
	lines := strings.Split(chunk, "\n")
	if len(lines) == 0 {
		return
	}

	fd := &FileDiff{}
	oldName := ""
	foundPlusPlus := false

	// 按行扫描，提取文件名并解析 hunk
	i := 0
	for i < len(lines) {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "--- "):
			// 提取旧文件名（可能是 a/path 或 /dev/null）
			oldName = strings.TrimPrefix(line, "--- ")
			oldName = strings.TrimPrefix(oldName, "a/")
			if oldName == "/dev/null" {
				oldName = ""
			}
		case strings.HasPrefix(line, "+++ "):
			val := strings.TrimPrefix(line, "+++ ")
			if val == "/dev/null" {
				fd.IsDelete = true
				// 旧文件名作为键
				fd.NewName = oldName
			} else {
				fd.NewName = strings.TrimPrefix(val, "b/")
			}
			foundPlusPlus = true
		case strings.HasPrefix(line, "Binary files"):
			// 二进制文件，跳过整个块
			return
		case strings.HasPrefix(line, "@@ "):
			if !foundPlusPlus {
				i++
				continue
			}
			hunk, consumed := parseHunk(lines, i, len(fd.Hunks))
			if hunk != nil {
				fd.Hunks = append(fd.Hunks, *hunk)
			}
			i += consumed
			continue
		}
		i++
	}

	if !foundPlusPlus {
		return
	}

	// 使用文件名（新文件名或旧文件名）作为 map key
	key := fd.NewName
	if key == "" {
		key = oldName
	}
	if key != "" {
		dm.files[key] = fd
	}
}

// parseHunk 从 lines[start] 开始解析一个 hunk，返回 HunkRange 和消耗的行数。
// hunkIndex 是当前 hunk 在文件中的序号（0-based），用于计算 DiffOffset。
func parseHunk(lines []string, start int, hunkIndex int) (*HunkRange, int) {
	header := lines[start]
	newStart, newCount := parseHunkHeader(header)
	if newStart < 0 {
		return nil, 1
	}

	hunk := &HunkRange{
		NewStart: newStart,
		NewCount: newCount,
	}

	// DiffOffset 仅记录 hunk 序号备用，语义 A 的 MapLine 不直接使用
	hunk.DiffOffset = hunkIndex

	consumed := 1

	for idx := start + 1; idx < len(lines); idx++ {
		line := lines[idx]
		// 遇到下一个 hunk header 或新文件 diff 块则停止
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "diff --git ") {
			break
		}
		consumed++
		// 语义 A 仅需 NewStart + NewCount 进行范围判断，
		// 不填充 Lines 切片以节省内存（Reserved for Semantic B）
	}

	return hunk, consumed
}

// parseHunkHeader 解析 "@@ -a,b +c,d @@" 格式的 hunk header，
// 返回新文件起始行号和行数。失败时返回 (-1, 0)。
func parseHunkHeader(line string) (newStart, newCount int) {
	// 格式：@@ -oldStart[,oldCount] +newStart[,newCount] @@ [optional context]
	end := strings.Index(line[3:], "@@")
	if end < 0 {
		return -1, 0
	}
	middle := strings.TrimSpace(line[3 : 3+end])
	parts := strings.Fields(middle)
	// 找 +... 部分
	for _, p := range parts {
		if strings.HasPrefix(p, "+") {
			spec := p[1:]
			if idx := strings.Index(spec, ","); idx >= 0 {
				ns, err1 := strconv.Atoi(spec[:idx])
				nc, err2 := strconv.Atoi(spec[idx+1:])
				if err1 != nil || err2 != nil {
					return -1, 0
				}
				return ns, nc
			}
			// 没有逗号，行数默认为 1
			ns, err := strconv.Atoi(spec)
			if err != nil {
				return -1, 0
			}
			return ns, 1
		}
	}
	return -1, 0
}

// MapLine 将新文件中的行号映射为该行在 diff 中的 position。
// 语义 A：直接返回 line 作为 position，仅校验行号是否落在某个 hunk 的新文件范围内。
// 若行号不在任何 hunk 范围内，或文件为删除文件，返回 (0, false)。
func (dm *DiffMap) MapLine(file string, line int) (position int, ok bool) {
	fd, exists := dm.files[file]
	if !exists || fd.IsDelete {
		return 0, false
	}

	for _, hunk := range fd.Hunks {
		hunkEnd := hunk.NewStart + hunk.NewCount - 1
		if hunk.NewCount == 0 {
			// 纯删除 hunk（新文件行数为 0），无可映射的行
			continue
		}
		if line >= hunk.NewStart && line <= hunkEnd {
			// 语义 A：直接返回行号作为 position
			return line, true
		}
	}
	return 0, false
}
