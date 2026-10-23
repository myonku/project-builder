package attachments

import (
	"context"
	"strings"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// MineFilter scopes every query to the authenticated uploader, never a request-supplied user.
type MineFilter struct {
	ActorID        string
	OriginalName   string
	ContainerType  string
	ProjectID      *string
	IncludeDeleted bool
	DeletedOnly    bool
}

type MineInput struct {
	Actor domain.User
	MineFilter
	Page listquery.PageRequest
	Sort string
}

type NamedSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type MineSource struct {
	Type     string       `json:"type"`
	Project  NamedSource  `json:"project"`
	Task     *NamedSource `json:"task"`
	ReadOnly bool         `json:"read_only"`
}
type MinePermissions struct {
	CanDelete bool `json:"can_delete"`
	CanPurge  bool `json:"can_purge"`
}
type MineRecord struct {
	Record
	Source      MineSource
	Permissions MinePermissions
}

// MineContext contains only batch-loaded permission facts; domain remains the authority.
type MineContext struct {
	Project           domain.Project
	Task              *domain.Task
	Group             domain.Group
	ProjectRole       *string
	GroupRole         *string
	GroupMemberStatus *string
}

func (c MineContext) permission(actor domain.User) domain.Permission {
	var member *domain.ProjectMember
	if c.ProjectRole != nil {
		member = &domain.ProjectMember{Role: *c.ProjectRole}
	}
	var groupMember *domain.GroupMember
	if c.GroupRole != nil && c.GroupMemberStatus != nil {
		groupMember = &domain.GroupMember{Role: *c.GroupRole, Status: *c.GroupMemberStatus}
	}
	permission := domain.ProjectPermission(c.Project, actor, member, c.Group, groupMember)
	if c.Task != nil {
		return domain.TaskPermission(*c.Task, c.Project, permission)
	}
	return permission
}
func (c MineContext) key() string {
	if c.Task != nil {
		return "task:" + c.Task.ID
	}
	return "project:" + c.Project.ID
}
func recordContainerKey(r Record) string {
	if r.TaskID != nil {
		return "task:" + *r.TaskID
	}
	return "project:" + *r.ProjectID
}

func (s *Service) ListMine(ctx context.Context, in MineInput) (Page[MineRecord], error) {
	in.MineFilter.ActorID = in.Actor.ID
	in.OriginalName = strings.TrimSpace(in.OriginalName)
	fields := map[string]string{}
	if len([]rune(in.OriginalName)) > domain.AttachmentNameMaxLength {
		fields["original_name"] = "文件名不得超过 255 个字符"
	}
	if in.ContainerType != "" && in.ContainerType != "project" && in.ContainerType != "task" {
		fields["container_type"] = "类型只能是 project 或 task"
	}
	if in.ProjectID != nil && !idgen.IsUUID(*in.ProjectID) {
		fields["project_id"] = "必须是合法的 UUID 字符串"
	}
	if in.IncludeDeleted && in.DeletedOnly {
		fields["deleted_only"] = "deleted_only 与 include_deleted 不能同时为真"
	}
	if len(fields) > 0 {
		return Page[MineRecord]{}, domain.ErrValidation.WithFields(fields)
	}
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[MineRecord]{}, err
	}
	order, err := AttachmentSort.Resolve(in.Sort)
	if err != nil {
		return Page[MineRecord]{}, err
	}
	contexts, err := s.repo.MineContexts(ctx, in.MineFilter)
	if err != nil {
		return Page[MineRecord]{}, err
	}
	projects, tasks := []string{}, []string{}
	visible := map[string]MineContext{}
	permissions := map[string]domain.Permission{}
	for _, c := range contexts {
		permission := c.permission(in.Actor)
		if !domain.CanReadAttachment(permission) {
			continue
		}
		visible[c.key()] = c
		permissions[c.key()] = permission
		if c.Task != nil {
			tasks = append(tasks, c.Task.ID)
		} else {
			projects = append(projects, c.Project.ID)
		}
	}
	result := Page[MineRecord]{Items: []MineRecord{}, Page: pagination}
	if len(visible) == 0 {
		return result, nil
	}
	result.Total, err = s.repo.CountMine(ctx, in.MineFilter, projects, tasks)
	if err != nil {
		return result, err
	}
	records, err := s.repo.ListMine(ctx, in.MineFilter, projects, tasks, AttachmentSort.OrderBy(order), pagination.Limit(), pagination.Offset())
	if err != nil {
		return result, err
	}
	for _, record := range records {
		key := recordContainerKey(record)
		c := visible[key]
		source := MineSource{Type: "project", Project: NamedSource{ID: c.Project.ID, Name: c.Project.Name}, ReadOnly: c.Project.IsReadOnly()}
		if c.Task != nil {
			source.Type = "task"
			source.Task = &NamedSource{ID: c.Task.ID, Name: c.Task.Name}
		}
		result.Items = append(result.Items, MineRecord{Record: record, Source: source, Permissions: MinePermissions{
			CanDelete: !record.IsDeleted() && domain.CanWriteAttachment(permissions[key], source.ReadOnly),
			CanPurge:  record.IsDeleted() && domain.CanViewDeletedAttachment(in.Actor, record.UploaderID, permissions[key]),
		}})
	}
	return result, nil
}
