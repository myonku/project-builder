package domain

import (
	"fmt"
	"path"
	"strings"
)

// 附件规则。本文件只放**纯函数**：命名规范化、扩展名与大小校验、可见性判定。
// 与数据库、HTTP、文件系统无关，因此可以表驱动测试。

// AttachmentNameMaxLength 必须与 backend/docs/B02-数据库模型与迁移.md 的列定义一致。
const AttachmentNameMaxLength = 255

// NormalizeAttachmentName 规范化用户提供的文件名，使其只作为**展示字段**安全可用。
//
// 关键点：本函数的输出**绝不参与存储路径的构造**（stored_key 由服务端独立生成）。
// 即使如此仍要处理，因为：
//   - 客户端可能回传完整路径（"C:\\Users\\x\\报表.xlsx"），直接展示会很难看；
//   - 文件名会被写进 Content-Disposition 响应头，必须去掉换行等可注入字符；
//   - 超长文件名会撞上列长度限制，退化成 500。
func NormalizeAttachmentName(raw string) string {
	// 同时处理两种分隔符：客户端可能来自任何平台。
	normalized := strings.ReplaceAll(raw, "\\", "/")
	normalized = path.Base(strings.TrimSpace(normalized))
	// 去掉控制字符（含换行），它们会破坏响应头。
	normalized = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, normalized)
	normalized = strings.TrimSpace(normalized)
	if normalized == "" || normalized == "." || normalized == "/" {
		normalized = "attachment"
	}

	// 按**字节**截断会切碎多字节字符，因此按码点截断；
	// 截断时保留扩展名，否则白名单与展示都会失真。
	runes := []rune(normalized)
	if len(runes) <= AttachmentNameMaxLength {
		return normalized
	}
	extension := path.Ext(normalized)
	keep := AttachmentNameMaxLength - len([]rune(extension))
	if keep < 1 {
		return string(runes[:AttachmentNameMaxLength])
	}
	return string(runes[:keep]) + extension
}

// AttachmentExtension 返回文件名的小写扩展名（含点号）；没有扩展名时返回空串。
func AttachmentExtension(name string) string {
	trimmed := strings.TrimSpace(name)
	extension := path.Ext(trimmed)
	if extension == "" || extension == "." {
		return ""
	}
	// ".hidden" 这种"整个文件名就是扩展名"的点文件视为没有扩展名：
	// 否则一个名为 ".pdf" 的文件会被当成 PDF 通过白名单，
	// 而展示上"扩展名等于整个文件名"也毫无意义。
	if len(extension) == len(trimmed) {
		return ""
	}
	return strings.ToLower(extension)
}

// ValidateAttachmentExtension 校验扩展名是否在白名单内。
//
// allowed 为空或含 "*" 表示不限制（需求文档 §9："扩展名白名单适当宽松"）。
// 不匹配时返回 415 unsupported_media_type——契约里已有该错误码。
func ValidateAttachmentExtension(extension string, allowed []string, unrestricted bool) error {
	if unrestricted {
		return nil
	}
	if extension == "" {
		return ErrUnsupportedMediaType.
			WithField("file", "该文件没有扩展名，而当前配置只接受白名单内的类型")
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), extension) {
			return nil
		}
	}
	return ErrUnsupportedMediaType.
		WithField("file", fmt.Sprintf("不支持的文件类型 %s", extension))
}

// ValidateAttachmentSize 校验单个文件大小。
//
// 超出上限返回 413 file_too_large（契约里已有该错误码）。
func ValidateAttachmentSize(size, maxSize int64) error {
	switch {
	case size <= 0:
		return ErrValidation.WithField("file", "文件内容为空")
	case maxSize > 0 && size > maxSize:
		return ErrFileTooLarge.
			WithField("file", fmt.Sprintf("文件不得超过 %d 字节", maxSize))
	}
	return nil
}

// CanReadAttachment 报告具备该容器权限的 actor 能否读取附件。
//
// 附件没有独立权限：它完全继承所在项目/任务的可读性。
func CanReadAttachment(permission Permission) bool { return permission != PermNone }

// CanWriteAttachment 报告 actor 能否上传或逻辑删除附件。
//
// 需要写权限，且容器必须可写：归档项目下的附件是只读的
// （与"归档项目对其下任务只读"同一条规则）。
func CanWriteAttachment(permission Permission, containerReadOnly bool) bool {
	if containerReadOnly {
		return false
	}
	return permission.AtLeast(PermWrite)
}

// CanViewDeletedAttachment 报告 actor 能否看到、以及能否彻底删除**已逻辑删除**的附件。
//
// 规则（用户确认）：逻辑删除后的附件只对**持有者**与**系统管理员**可见，
// 另有**上传者本人**——他可能是唯一还关心这个文件的人，而且彻底删除
// 正是为清理自己上传的文件准备的。
//
// 这里的"持有者"直接复用权限层级中的 PermManage：项目的持有者与任务的持有者
// 都恰好得到该级别，系统管理员经领域层放行后同样如此。因此不需要再单独判断
// "是不是持有者"，避免出现第二套判定。
func CanViewDeletedAttachment(actor User, uploaderID string, permission Permission) bool {
	if actor.IsAdmin() {
		return true
	}
	if actor.ID != "" && actor.ID == uploaderID {
		return true
	}
	return permission.AtLeast(PermManage)
}

// AttachmentQuotaError 返回配额已满的统一错误。
//
// 用 409 conflict 而不是 422：这不是字段格式问题，而是"当前状态不允许再新增"，
// 与重复成员、重复申请属于同一类。
func AttachmentQuotaError(limit int) error {
	return ErrConflict.WithField("file",
		fmt.Sprintf("该账号的附件数量已达上限（%d），请先彻底删除不再需要的附件", limit))
}
