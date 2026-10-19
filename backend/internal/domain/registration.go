package domain

// 注册申请状态（与 migration 000006 的 ck_registration_requests_status 一致）。
const (
	RegistrationStatusPending  = "pending"
	RegistrationStatusApproved = "approved"
	RegistrationStatusRejected = "rejected"
)

// RegistrationReviewReasonMaxLength 是拒绝原因的字符上限，与列定义一致。
const RegistrationReviewReasonMaxLength = 2000

// RegistrationStatuses 返回全部申请状态（供校验与文档断言使用）。
func RegistrationStatuses() []string {
	return []string{
		RegistrationStatusPending,
		RegistrationStatusApproved,
		RegistrationStatusRejected,
	}
}

// IsRegistrationStatus 报告取值是否为合法申请状态。
func IsRegistrationStatus(value string) bool {
	for _, item := range RegistrationStatuses() {
		if item == value {
			return true
		}
	}
	return false
}
