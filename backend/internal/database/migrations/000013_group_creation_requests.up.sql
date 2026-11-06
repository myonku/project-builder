CREATE TABLE group_creation_requests (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    applicant_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name VARCHAR(160) NOT NULL CHECK (length(btrim(name)) > 0),
    description VARCHAR(2000),
    reason VARCHAR(2000) NOT NULL CHECK (length(btrim(reason)) > 0),
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    reviewed_by UUID REFERENCES users(id) ON DELETE RESTRICT,
    reviewed_at TIMESTAMPTZ,
    rejection_reason VARCHAR(2000),
    group_id UUID UNIQUE REFERENCES groups(id) ON DELETE RESTRICT,
    CONSTRAINT ck_group_creation_requests_status CHECK (status IN ('pending','approved','rejected')),
    CONSTRAINT ck_group_creation_requests_review CHECK (
        (status='pending' AND reviewed_by IS NULL AND reviewed_at IS NULL AND rejection_reason IS NULL AND group_id IS NULL)
        OR (status='approved' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND rejection_reason IS NULL AND group_id IS NOT NULL)
        OR (status='rejected' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL AND rejection_reason IS NOT NULL AND length(btrim(rejection_reason))>0 AND group_id IS NULL)
    )
);
CREATE UNIQUE INDEX uq_group_creation_requests_pending_name
    ON group_creation_requests(applicant_id,lower(name)) WHERE status='pending';
CREATE INDEX ix_group_creation_requests_applicant_created ON group_creation_requests(applicant_id,created_at DESC,id);
CREATE INDEX ix_group_creation_requests_status_created ON group_creation_requests(status,created_at,id);

ALTER TABLE activity_logs ALTER COLUMN object_type TYPE VARCHAR(32);
ALTER TABLE activity_logs DROP CONSTRAINT ck_activity_logs_object_type;
ALTER TABLE activity_logs ADD CONSTRAINT ck_activity_logs_object_type CHECK (
    object_type IN ('user','group','project','task','attachment','reminder','group_creation_request')
);
ALTER TABLE activity_logs DROP CONSTRAINT ck_activity_logs_action;
ALTER TABLE activity_logs ADD CONSTRAINT ck_activity_logs_action CHECK (
    action IN ('created','updated','archived','deleted','purged','member_added','member_role_changed',
        'member_removed','owner_transferred','join_request_reviewed','shared','password_reset','disabled','group_creation_reviewed')
);
