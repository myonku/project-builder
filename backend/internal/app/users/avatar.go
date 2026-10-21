package users

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码器
	_ "image/png"  // 注册 PNG 解码器
	"io"
	"net/http"
	"time"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/storage"
)

// 头像策略（P01 §4.4，2026-10-21 决策：不支持 WebP）。
const (
	defaultAvatarMaxFileSize int64 = 2 << 20 // 2 MiB
	avatarMaxDimension             = 4096
	avatarMimePNG                  = "image/png"
	avatarMimeJPEG                 = "image/jpeg"
)

// AvatarContent 是头像下载的返回值。
type AvatarContent struct {
	Reader    io.ReadCloser
	Mime      string
	SizeBytes int64
	UpdatedAt time.Time
}

// UploadAvatarInput 是上传头像的用例输入。
type UploadAvatarInput struct {
	Actor domain.User
	Data  []byte
}

// AvatarMaxFileSize 返回头像上传的字节上限（HTTP 层据此设置 MaxBytesReader）。
func (s *Service) AvatarMaxFileSize() int64 { return s.avatarMaxFileSize }

// Avatar 返回目标用户的头像字节。
//
// 可见性与用户资料完全一致（本人 / 管理员 / 同工作组或同项目）；
// 不可见与不存在同为 user_not_found，可见但没上传头像为 avatar_not_found。
func (s *Service) Avatar(ctx context.Context, actor domain.User, targetID string) (AvatarContent, error) {
	record, err := s.requireVisible(ctx, actor, targetID)
	if err != nil {
		return AvatarContent{}, err
	}
	if record.AvatarStoredKey == nil || record.AvatarUpdatedAt == nil {
		return AvatarContent{}, domain.ErrAvatarNotFound
	}
	if s.storage == nil {
		return AvatarContent{}, domain.ErrInternal.WithCause(errNoAvatarStorage)
	}

	reader, err := s.storage.Open(ctx, *record.AvatarStoredKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			// 数据库有行、对象缺失：按"没有头像"返回，并留下排查线索。
			s.logger.Warn("头像对象缺失",
				"user_id", record.ID,
				"cause", err,
			)
			return AvatarContent{}, domain.ErrAvatarNotFound
		}
		return AvatarContent{}, err
	}

	return AvatarContent{
		Reader:    reader,
		Mime:      record.AvatarMime,
		SizeBytes: record.AvatarSizeBytes,
		UpdatedAt: *record.AvatarUpdatedAt,
	}, nil
}

// UploadAvatar 校验并保存本人头像，返回更新后的公开资料。
//
// 顺序：校验字节（415/422）→ 写新对象 → 事务替换数据库行 → 提交后删旧对象。
// 数据库写失败时删除刚写入的新对象，避免留下无行引用的孤儿。
func (s *Service) UploadAvatar(ctx context.Context, in UploadAvatarInput) (ProfileRecord, error) {
	if s.storage == nil {
		return ProfileRecord{}, domain.ErrInternal.WithCause(errNoAvatarStorage)
	}

	mime, err := validateAvatar(in.Data, s.avatarMaxFileSize)
	if err != nil {
		return ProfileRecord{}, err
	}

	objectID, err := s.ids.UUID()
	if err != nil {
		return ProfileRecord{}, domain.ErrInternal.WithCause(err)
	}
	key := "avatars/" + in.Actor.ID + "/" + objectID

	if err := s.storage.Put(ctx, key, bytes.NewReader(in.Data), int64(len(in.Data))); err != nil {
		return ProfileRecord{}, fmt.Errorf("写入头像对象失败: %w", err)
	}

	record, oldKey, err := s.repo.ReplaceAvatar(ctx, AvatarUpdateParams{
		UserID:    in.Actor.ID,
		StoredKey: &key,
		Mime:      mime,
		SizeBytes: int64(len(in.Data)),
		Audit: s.auditWith(in.Actor.ID, domain.AuditActionUpdated, in.Actor.ID,
			map[string]string{"avatar": "set"}),
	})
	if err != nil {
		// 数据库失败：刚写入的对象没有行引用，必须删除。
		if delErr := s.storage.Delete(ctx, key); delErr != nil {
			s.logger.Warn("回滚新头像对象失败",
				"user_id", in.Actor.ID,
				"cause", delErr,
			)
		}
		return ProfileRecord{}, err
	}

	if oldKey != nil {
		if err := s.storage.Delete(ctx, *oldKey); err != nil {
			// 旧行已失效，删除失败只影响磁盘回收；不能把成功的上传变成失败。
			s.logger.Warn("删除旧头像对象失败",
				"user_id", in.Actor.ID,
				"cause", err,
			)
		}
	}

	s.logger.Info("用户头像已更新",
		"user_id", in.Actor.ID,
		"mime", mime,
		"size_bytes", len(in.Data),
	)
	return record, nil
}

// RemoveAvatar 清除本人头像；没有头像时是幂等的空操作。
//
// 先清数据库、后删对象：即使对象删除失败，行已不再引用它，页面不会显示旧图。
func (s *Service) RemoveAvatar(ctx context.Context, actor domain.User) error {
	if s.storage == nil {
		return domain.ErrInternal.WithCause(errNoAvatarStorage)
	}

	current, err := s.repo.FindProfile(ctx, actor.ID)
	if err != nil {
		return err
	}
	if current.AvatarStoredKey == nil {
		return nil
	}

	_, oldKey, err := s.repo.ReplaceAvatar(ctx, AvatarUpdateParams{
		UserID: actor.ID,
		Audit: s.auditWith(actor.ID, domain.AuditActionUpdated, actor.ID,
			map[string]string{"avatar": "cleared"}),
	})
	if err != nil {
		return err
	}

	if oldKey != nil {
		if err := s.storage.Delete(ctx, *oldKey); err != nil {
			s.logger.Warn("删除被清除的头像对象失败",
				"user_id", actor.ID,
				"cause", err,
			)
		}
	}
	s.logger.Info("用户头像已清除", "user_id", actor.ID)
	return nil
}

// validateAvatar 对上传字节做真实格式、MIME 与尺寸校验，返回服务端认定的 MIME。
//
// 只信任内容嗅探与 image.DecodeConfig，不信任客户端提供的 Content-Type 或扩展名：
// 后者可以被任意伪造，而浏览器最终按服务端返回的 Content-Type 解释字节。
func validateAvatar(data []byte, maxBytes int64) (string, error) {
	if len(data) == 0 {
		return "", domain.ErrValidation.WithField("file", "头像文件不能为空")
	}
	if int64(len(data)) > maxBytes {
		return "", domain.ErrFileTooLarge.
			WithField("file", fmt.Sprintf("头像不得超过 %d 字节", maxBytes))
	}

	contentType := http.DetectContentType(data)
	var expectedFormat, mime string
	switch contentType {
	case avatarMimePNG:
		expectedFormat, mime = "png", avatarMimePNG
	case avatarMimeJPEG:
		expectedFormat, mime = "jpeg", avatarMimeJPEG
	default:
		return "", domain.ErrUnsupportedMediaType.
			WithField("file", "头像仅支持 PNG 或 JPEG")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", domain.ErrUnsupportedMediaType.
			WithField("file", "无法解析图片内容，仅支持 PNG 或 JPEG")
	}
	if format != expectedFormat || config.Width <= 0 || config.Height <= 0 {
		return "", domain.ErrUnsupportedMediaType.
			WithField("file", "图片内容与声明格式不一致，仅支持 PNG 或 JPEG")
	}
	if config.Width > avatarMaxDimension || config.Height > avatarMaxDimension {
		return "", domain.ErrValidation.
			WithField("file", fmt.Sprintf("头像尺寸不得超过 %d×%d", avatarMaxDimension, avatarMaxDimension))
	}
	return mime, nil
}

// errNoAvatarStorage 表示装配遗漏。
var errNoAvatarStorage = errors.New("users: 未注入头像存储")
