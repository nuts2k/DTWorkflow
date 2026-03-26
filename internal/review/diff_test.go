package review

import (
	"strings"
	"testing"
)

// singleFileOnHunk 单文件单 hunk 的标准 unified diff
const singleFileOneHunk = `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+
+import "fmt"

-func main() {}
+func main() { fmt.Println("hello") }
`

// singleFileMultiHunk 单文件多 hunk
const singleFileMultiHunk = `diff --git a/bar.go b/bar.go
index 000..111 100644
--- a/bar.go
+++ b/bar.go
@@ -1,3 +1,3 @@
 line1
-line2
+line2_new
 line3
@@ -10,3 +10,4 @@
 line10
+line10_added
 line11
 line12
`

// multiFileDiff 多文件 diff
const multiFileDiff = `diff --git a/alpha.go b/alpha.go
index aaa..bbb 100644
--- a/alpha.go
+++ b/alpha.go
@@ -1,2 +1,3 @@
 package alpha
+// added comment

diff --git a/beta.go b/beta.go
index ccc..ddd 100644
--- a/beta.go
+++ b/beta.go
@@ -5,3 +5,3 @@
 line5
-line6_old
+line6_new
 line7
`

// newFileDiff 新增文件（/dev/null -> b/path）
const newFileDiff = `diff --git a/new.go b/new.go
new file mode 100644
index 000..abc
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package new
+
+// new file
`

// deleteFileDiff 删除文件（a/path -> /dev/null）
const deleteFileDiff = `diff --git a/old.go b/old.go
deleted file mode 100644
index abc..000
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package old
-
-// old file
`

// binaryFileDiff 包含二进制文件的 diff（应跳过）
const binaryFileDiff = `diff --git a/image.png b/image.png
index abc..def 100644
Binary files a/image.png and b/image.png differ
diff --git a/code.go b/code.go
index 111..222 100644
--- a/code.go
+++ b/code.go
@@ -1,2 +1,3 @@
 package code
+// added

`

// TestParseDiff_SingleFileOneHunk 单文件单 hunk 基本解析正确性
func TestParseDiff_SingleFileOneHunk(t *testing.T) {
	dm := ParseDiff(singleFileOneHunk)
	if dm == nil {
		t.Fatal("ParseDiff 返回 nil")
	}

	fd, ok := dm.files["foo.go"]
	if !ok {
		t.Fatalf("未找到文件 foo.go，files=%v", dm.files)
	}
	if fd.IsDelete {
		t.Error("foo.go 不应被标记为删除")
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("期望 1 个 hunk，得到 %d", len(fd.Hunks))
	}

	hunk := fd.Hunks[0]
	if hunk.NewStart != 1 {
		t.Errorf("hunk.NewStart 期望 1，得到 %d", hunk.NewStart)
	}
	if hunk.NewCount != 4 {
		t.Errorf("hunk.NewCount 期望 4，得到 %d", hunk.NewCount)
	}
	// 语义 A 不填充 Lines 切片（Reserved for Semantic B）
	if len(hunk.Lines) != 0 {
		t.Errorf("语义 A 下 hunk.Lines 应为空，得到 %d 条", len(hunk.Lines))
	}
}

// TestParseDiff_SingleFileOneHunk_MapLine 行号映射准确性
func TestParseDiff_SingleFileOneHunk_MapLine(t *testing.T) {
	dm := ParseDiff(singleFileOneHunk)

	// 行 1 在 hunk 范围内（newStart=1, newCount=4 → 1..4）
	pos, ok := dm.MapLine("foo.go", 1)
	if !ok {
		t.Error("行 1 应在 hunk 范围内")
	}
	if pos != 1 {
		t.Errorf("语义 A：position 期望 1，得到 %d", pos)
	}

	// 行 4 在范围内
	pos, ok = dm.MapLine("foo.go", 4)
	if !ok {
		t.Error("行 4 应在 hunk 范围内")
	}
	if pos != 4 {
		t.Errorf("语义 A：position 期望 4，得到 %d", pos)
	}

	// 行 5 超出范围
	_, ok = dm.MapLine("foo.go", 5)
	if ok {
		t.Error("行 5 超出 hunk 范围，MapLine 应返回 false")
	}
}

// TestParseDiff_SingleFileMultiHunk 单文件多 hunk
func TestParseDiff_SingleFileMultiHunk(t *testing.T) {
	dm := ParseDiff(singleFileMultiHunk)

	fd, ok := dm.files["bar.go"]
	if !ok {
		t.Fatal("未找到文件 bar.go")
	}
	if len(fd.Hunks) != 2 {
		t.Fatalf("期望 2 个 hunk，得到 %d", len(fd.Hunks))
	}

	// 第一个 hunk：newStart=1, newCount=3
	h0 := fd.Hunks[0]
	if h0.NewStart != 1 || h0.NewCount != 3 {
		t.Errorf("hunk[0] 期望 start=1,count=3，得到 start=%d,count=%d", h0.NewStart, h0.NewCount)
	}

	// 第二个 hunk：newStart=10, newCount=4
	h1 := fd.Hunks[1]
	if h1.NewStart != 10 || h1.NewCount != 4 {
		t.Errorf("hunk[1] 期望 start=10,count=4，得到 start=%d,count=%d", h1.NewStart, h1.NewCount)
	}

	// 行 1 在第一个 hunk（1..3）
	pos, ok := dm.MapLine("bar.go", 1)
	if !ok {
		t.Error("行 1 应在第一个 hunk 范围内")
	}
	if pos != 1 {
		t.Errorf("position 期望 1，得到 %d", pos)
	}

	// 行 11 在第二个 hunk（10..13）
	pos, ok = dm.MapLine("bar.go", 11)
	if !ok {
		t.Error("行 11 应在第二个 hunk 范围内")
	}
	if pos != 11 {
		t.Errorf("position 期望 11，得到 %d", pos)
	}

	// 行 5 不在任何 hunk（hunk1:1-3, hunk2:10-13）
	_, ok = dm.MapLine("bar.go", 5)
	if ok {
		t.Error("行 5 不在任何 hunk 范围，应返回 false")
	}
}

// TestParseDiff_MultiFile 多文件 diff 文件名提取与按文件查询
func TestParseDiff_MultiFile(t *testing.T) {
	dm := ParseDiff(multiFileDiff)

	if _, ok := dm.files["alpha.go"]; !ok {
		t.Error("未找到 alpha.go")
	}
	if _, ok := dm.files["beta.go"]; !ok {
		t.Error("未找到 beta.go")
	}

	// alpha.go hunk: newStart=1,newCount=3 → 行 2 在范围
	_, ok := dm.MapLine("alpha.go", 2)
	if !ok {
		t.Error("alpha.go 行 2 应在 hunk 范围内")
	}

	// beta.go hunk: newStart=5,newCount=3 → 行 5 在范围
	_, ok = dm.MapLine("beta.go", 5)
	if !ok {
		t.Error("beta.go 行 5 应在 hunk 范围内")
	}
}

// TestParseDiff_NewFile 新增文件（/dev/null -> b/path）
func TestParseDiff_NewFile(t *testing.T) {
	dm := ParseDiff(newFileDiff)

	fd, ok := dm.files["new.go"]
	if !ok {
		t.Fatal("未找到 new.go")
	}
	if fd.IsDelete {
		t.Error("new.go 不应被标记为删除")
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("期望 1 个 hunk，得到 %d", len(fd.Hunks))
	}

	h := fd.Hunks[0]
	// @@ -0,0 +1,3 @@ → newStart=1, newCount=3
	if h.NewStart != 1 || h.NewCount != 3 {
		t.Errorf("hunk 期望 start=1,count=3，得到 start=%d,count=%d", h.NewStart, h.NewCount)
	}

	// 行 1 可映射
	pos, ok := dm.MapLine("new.go", 1)
	if !ok {
		t.Error("new.go 行 1 应可映射")
	}
	if pos != 1 {
		t.Errorf("position 期望 1，得到 %d", pos)
	}
}

// TestParseDiff_DeleteFile 删除文件标记为 IsDelete，MapLine 返回 false
func TestParseDiff_DeleteFile(t *testing.T) {
	dm := ParseDiff(deleteFileDiff)

	fd, ok := dm.files["old.go"]
	if !ok {
		t.Fatal("未找到 old.go")
	}
	if !fd.IsDelete {
		t.Error("old.go 应被标记为删除")
	}

	// 删除文件 MapLine 始终返回 false
	_, ok = dm.MapLine("old.go", 1)
	if ok {
		t.Error("删除文件 MapLine 应返回 false")
	}
}

// TestParseDiff_LineInRange 行号在 hunk 范围内返回 (position, true)
func TestParseDiff_LineInRange(t *testing.T) {
	dm := ParseDiff(singleFileOneHunk)

	// hunk range: 1..4
	for line := 1; line <= 4; line++ {
		pos, ok := dm.MapLine("foo.go", line)
		if !ok {
			t.Errorf("行 %d 应在范围内", line)
		}
		if pos != line {
			t.Errorf("行 %d：position 期望 %d，得到 %d", line, line, pos)
		}
	}
}

// TestParseDiff_LineOutOfRange 行号不在 hunk 范围内返回 (0, false)
func TestParseDiff_LineOutOfRange(t *testing.T) {
	dm := ParseDiff(singleFileOneHunk)

	// hunk range: 1..4，行 0 和行 5+ 都不在范围
	outOfRange := []int{0, 5, 100}
	for _, line := range outOfRange {
		pos, ok := dm.MapLine("foo.go", line)
		if ok {
			t.Errorf("行 %d 不在范围内，MapLine 应返回 false", line)
		}
		if pos != 0 {
			t.Errorf("行 %d 不在范围：position 期望 0，得到 %d", line, pos)
		}
	}
}

// TestParseDiff_EmptyDiff 空 diff 返回空 DiffMap，不 panic
func TestParseDiff_EmptyDiff(t *testing.T) {
	for _, input := range []string{"", "   ", "\n\n"} {
		dm := ParseDiff(input)
		if dm == nil {
			t.Fatal("ParseDiff 对空输入返回 nil")
		}
		if len(dm.files) != 0 {
			t.Errorf("空 diff 应返回空 DiffMap，得到 %d 个文件", len(dm.files))
		}
		// MapLine 不应 panic
		_, ok := dm.MapLine("any.go", 1)
		if ok {
			t.Error("空 DiffMap 的 MapLine 应返回 false")
		}
	}
}

// TestParseDiff_BinaryFile 二进制文件 diff 跳过，不影响其他文件
func TestParseDiff_BinaryFile(t *testing.T) {
	dm := ParseDiff(binaryFileDiff)

	// image.png（二进制）不应出现在 map 中
	if _, ok := dm.files["image.png"]; ok {
		t.Error("二进制文件 image.png 不应被解析到 DiffMap")
	}

	// code.go 应正常解析
	fd, ok := dm.files["code.go"]
	if !ok {
		t.Fatal("code.go 未被解析")
	}
	if len(fd.Hunks) == 0 {
		t.Error("code.go 应有至少一个 hunk")
	}
}

// TestParseDiff_UnknownFile MapLine 对不存在的文件名返回 false
func TestParseDiff_UnknownFile(t *testing.T) {
	dm := ParseDiff(singleFileOneHunk)
	_, ok := dm.MapLine("nonexistent.go", 1)
	if ok {
		t.Error("不存在的文件 MapLine 应返回 false")
	}
}

// TestParseDiff_OversizedInput 超过 maxDiffSize 的输入返回空 DiffMap，不 OOM
func TestParseDiff_OversizedInput(t *testing.T) {
	// 构造一个略超过 maxDiffSize 的输入
	oversized := strings.Repeat("x", maxDiffSize+1)
	dm := ParseDiff(oversized)
	if dm == nil {
		t.Fatal("ParseDiff 对超大输入返回 nil")
	}
	if len(dm.files) != 0 {
		t.Errorf("超大输入应返回空 DiffMap，得到 %d 个文件", len(dm.files))
	}
}

// TestParseDiff_ExactMaxSize 刚好等于 maxDiffSize 的输入应正常解析
func TestParseDiff_ExactMaxSize(t *testing.T) {
	// 构造一个刚好等于 maxDiffSize 的输入（内容无效 diff，但不应被大小限制拦截）
	exact := strings.Repeat("x", maxDiffSize)
	dm := ParseDiff(exact)
	if dm == nil {
		t.Fatal("ParseDiff 对恰好 maxDiffSize 大小输入返回 nil")
	}
	// 内容不是合法 diff，所以文件数为 0，但不应被大小检查拦截
}
