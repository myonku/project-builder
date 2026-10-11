package auth

import (
	"context"
	"time"

	"github.com/myonku/project-builder/internal/domain"
)

// 本文件是本模块给**装配点**（cmd/api 或测试装配）使用的适配入口。
//
// 使用场景：app/users 的"管理员代建账号"与"管理员重置密码"都是账号级操作，
// 必须复用本模块已有的注册与密码写入逻辑，而不是把归一化、密码强度、
// 唯一性检查与约束翻译复制一份。app/users 只声明自己需要的最小接口
// （users.Accounts），由装配点把 *Service 注入进去，两个业务模块之间
// 因此不产生 import 依赖。
//
// 与 projects.AccessFor 的处理方式一致：适配逻辑本身只做字段搬运与调用转发，
// 如果这里出现业务判断，说明边界划错了。

// CreateAccount 创建一个账号，语义与用户自助注册完全一致。
//
// 存在的唯一理由：让"管理员代建账号"与"自助注册"共用同一套实现。
// 两者若各自实现，密码强度、用户名归一化与唯一性检查迟早会分叉，
// 而这三项都属于安全边界。
func (s *Service) CreateAccount(
	ctx context.Context, in domain.RegistrationInput,
) (domain.User, error) {
	result, err := s.Register(ctx, in)
	if err != nil {
		return domain.User{}, err
	}
	return result.User.ToDomain(), nil
}

// ResetPassword 重置指定用户的密码，要求提交当前版本。
//
// 为什么不在这里记日志：日志需要同时记录"哪个管理员重置了哪个用户"，
// 而本方法拿不到操作者——调用方（users 端点）才持有当前用户。
// 让调用方记录完整的一条，比在这里记一条缺胳膊少腿的更有价值。
//
// 注意：本方法**不**使该用户已有的会话失效。原因与替代方案见
// docs/06-迁移进度与验证.md 7.4 第 1 项；需要立即踢下线时可先停用再启用该账号
// （停用会让既有会话在下一次请求时立即失效）。
func (s *Service) ResetPassword(
	ctx context.Context, userID, newPassword string, expectedUpdated time.Time,
) (domain.User, error) {
	if _, fields := domain.NormalizeNewPassword(newPassword, s.security.PasswordMinLength); len(fields) > 0 {
		return domain.User{}, domain.ErrValidation.WithFields(fields)
	}

	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return domain.User{}, domain.ErrInternal.WithCause(err)
	}

	record, err := s.users.UpdatePasswordWithCAS(ctx, userID, hash, expectedUpdated)
	if err != nil {
		return domain.User{}, err
	}
	return record.ToDomain(), nil
}
