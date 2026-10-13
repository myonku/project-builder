package domain

import (
	"strings"
	"testing"
)

// TestNormalizeAttachmentName 覆盖文件名规范化。
//
// 这个函数是"用户输入只作为展示字段"这条规则的第一道闸门：
// 它的输出会进数据库、进响应头，因此必须挡住路径、控制字符与超长。
func TestNormalizeAttachmentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "普通文件名保持不变", raw: "report.pdf", want: "report.pdf"},
		{name: "保留中文", raw: "季度总结 2026.xlsx", want: "季度总结 2026.xlsx"},
		{name: "剥离 Windows 绝对路径", raw: `C:\Users\me\Desktop\报表.xlsx`, want: "报表.xlsx"},
		{name: "剥离 POSIX 绝对路径", raw: "/var/tmp/notes.md", want: "notes.md"},
		{name: "剥离相对路径", raw: "../../etc/passwd", want: "passwd"},
		{name: "去掉换行（否则会破坏响应头）", raw: "a\r\nb.txt", want: "ab.txt"},
		{name: "去掉控制字符", raw: "a\x00\x1fb.txt", want: "ab.txt"},
		{name: "纯空白回退为默认名", raw: "   ", want: "attachment"},
		{name: "当前目录回退为默认名", raw: ".", want: "attachment"},
		{name: "空字符串回退为默认名", raw: "", want: "attachment"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeAttachmentName(tc.raw); got != tc.want {
				t.Fatalf("NormalizeAttachmentName(%q) = %q，期望 %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNormalizeAttachmentNameTruncates 覆盖超长文件名的截断。
func TestNormalizeAttachmentNameTruncates(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("名", 400) + ".pdf"
	got := NormalizeAttachmentName(long)
	if len([]rune(got)) > AttachmentNameMaxLength {
		t.Fatalf("截断后长度 = %d，期望不超过 %d", len([]rune(got)), AttachmentNameMaxLength)
	}
	// 必须保留扩展名：否则白名单与展示都会失真。
	if !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("截断后应保留扩展名，实际 %q", got)
	}
	// 按码点截断：不能出现半个多字节字符（表现为 U+FFFD）。
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("截断切碎了多字节字符: %q", got)
	}
}

// TestAttachmentExtension 覆盖扩展名提取。
func TestAttachmentExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "report.PDF", want: ".pdf"},
		{name: "archive.tar.gz", want: ".gz"},
		{name: "no-extension", want: ""},
		{name: "trailing.dot.", want: ""},
		{name: ".hidden", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AttachmentExtension(tc.name); got != tc.want {
				t.Fatalf("AttachmentExtension(%q) = %q，期望 %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestValidateAttachmentExtension 覆盖白名单判定。
func TestValidateAttachmentExtension(t *testing.T) {
	t.Parallel()

	allowed := []string{".pdf", ".docx"}

	tests := []struct {
		name         string
		extension    string
		unrestricted bool
		wantErr      bool
	}{
		{name: "命中白名单", extension: ".pdf"},
		{name: "命中时忽略大小写", extension: ".PDF"},
		{name: "不在白名单", extension: ".exe", wantErr: true},
		{name: "没有扩展名", extension: "", wantErr: true},
		{name: "不限制时任何扩展名都通过", extension: ".exe", unrestricted: true},
		{name: "不限制时无扩展名也通过", extension: "", unrestricted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAttachmentExtension(tc.extension, allowed, tc.unrestricted)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

// TestValidateAttachmentSize 覆盖大小校验。
func TestValidateAttachmentSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		size    int64
		max     int64
		wantErr bool
	}{
		{name: "正常大小", size: 1024, max: 4096},
		{name: "恰好等于上限（边界应通过）", size: 4096, max: 4096},
		{name: "超过上限", size: 4097, max: 4096, wantErr: true},
		{name: "空文件", size: 0, max: 4096, wantErr: true},
		{name: "负数", size: -1, max: 4096, wantErr: true},
		{name: "上限为 0 表示不限制", size: 1 << 30, max: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAttachmentSize(tc.size, tc.max)
			if tc.wantErr && err == nil {
				t.Fatal("期望错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望无错误，实际 %v", err)
			}
		})
	}
}

// TestAttachmentWritePermission 覆盖"能否上传/删除附件"。
func TestAttachmentWritePermission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		permisson Permission
		readOnly  bool
		want      bool
	}{
		{name: "写权限可上传", permisson: PermWrite, want: true},
		{name: "管理权限可上传", permisson: PermManage, want: true},
		{name: "只读权限不可上传", permisson: PermRead},
		{name: "无权限不可上传", permisson: PermNone},
		// 归档项目只读：即使持有者（PermManage）也不能上传。
		{name: "归档项目连持有者也不可上传", permisson: PermManage, readOnly: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanWriteAttachment(tc.permisson, tc.readOnly); got != tc.want {
				t.Fatalf("CanWriteAttachment = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestCanViewDeletedAttachment 覆盖"逻辑删除后谁能看到"。
//
// 人工确认的规则：上传者、持有者（PermManage）与系统管理员。
func TestCanViewDeletedAttachment(t *testing.T) {
	t.Parallel()

	admin := User{ID: "admin", SystemRole: UserSystemRoleAdmin}
	uploader := User{ID: "uploader"}
	holder := User{ID: "holder"}
	other := User{ID: "other"}

	tests := []struct {
		name       string
		actor      User
		uploaderID string
		permission Permission
		want       bool
	}{
		{name: "上传者可见", actor: uploader, uploaderID: "uploader", permission: PermWrite, want: true},
		{name: "持有者可见", actor: holder, uploaderID: "uploader", permission: PermManage, want: true},
		{name: "系统管理员可见", actor: admin, uploaderID: "uploader", permission: PermManage, want: true},
		// 管理员即使权限被算成 PermNone（例如容器访问器实现有出入）也必须可见：
		// 这一条是"管理员兜底"在本模块的体现。
		{name: "系统管理员无需权限层级", actor: admin, uploaderID: "uploader", permission: PermNone, want: true},
		{name: "其他普通成员不可见", actor: other, uploaderID: "uploader", permission: PermWrite},
		{name: "只读成员不可见", actor: other, uploaderID: "uploader", permission: PermRead},
		{name: "无权限者不可见", actor: other, uploaderID: "uploader", permission: PermNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CanViewDeletedAttachment(tc.actor, tc.uploaderID, tc.permission)
			if got != tc.want {
				t.Fatalf("CanViewDeletedAttachment = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestAttachmentQuotaErrorIsConflict 锁住配额错误的语义与错误码。
func TestAttachmentQuotaErrorIsConflict(t *testing.T) {
	t.Parallel()

	err := AttachmentQuotaError(1000)
	if domainErr := AsError(err); domainErr == nil || domainErr.Code != CodeConflict {
		t.Fatalf("配额错误的码 = %v，期望 %q", AsError(err), CodeConflict)
	}
	// 错误信息必须带上上限值，否则用户不知道要清理到多少。
	if !strings.Contains(err.Error(), "1000") {
		t.Fatalf("错误信息应包含上限值，实际 %q", err.Error())
	}
}
