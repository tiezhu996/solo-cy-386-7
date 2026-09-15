-- 001_init.sql 参考迁移脚本：GORM AutoMigrate 会生成等价结构。
-- users
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(32) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(32) NOT NULL,
    email VARCHAR(128),
    phone VARCHAR(20),
    avatar VARCHAR(512),
    role VARCHAR(16) NOT NULL DEFAULT 'user',
    credit_score INTEGER NOT NULL DEFAULT 100,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
-- products
CREATE TABLE IF NOT EXISTS products (
    id BIGSERIAL PRIMARY KEY,
    seller_id BIGINT NOT NULL,
    title VARCHAR(128) NOT NULL,
    description TEXT NOT NULL,
    original_price NUMERIC(12,2) NOT NULL,
    price NUMERIC(12,2) NOT NULL,
    condition VARCHAR(32) NOT NULL DEFAULT 'almost_new',
    category VARCHAR(32) NOT NULL DEFAULT 'other',
    images TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'on_sale',
    view_count INTEGER NOT NULL DEFAULT 0,
    favorite_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
-- favorites
CREATE TABLE IF NOT EXISTS favorites (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_fav_user_product UNIQUE (user_id, product_id)
);
-- addresses
CREATE TABLE IF NOT EXISTS addresses (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    receiver_name VARCHAR(32) NOT NULL,
    phone VARCHAR(20) NOT NULL,
    province VARCHAR(32) NOT NULL,
    city VARCHAR(32) NOT NULL,
    district VARCHAR(32),
    detail VARCHAR(255) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- cart_items
CREATE TABLE IF NOT EXISTS cart_items (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    selected BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- orders
CREATE TABLE IF NOT EXISTS orders (
    id BIGSERIAL PRIMARY KEY,
    order_no VARCHAR(40) NOT NULL UNIQUE,
    buyer_id BIGINT NOT NULL,
    seller_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    address_id BIGINT NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    total_price NUMERIC(12,2) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending_payment',
    remark VARCHAR(255),
    paid_at TIMESTAMPTZ,
    shipped_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
-- messages
CREATE TABLE IF NOT EXISTS messages (
    id BIGSERIAL PRIMARY KEY,
    sender_id BIGINT NOT NULL,
    receiver_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    content VARCHAR(1000) NOT NULL,
    is_read BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- reviews
CREATE TABLE IF NOT EXISTS reviews (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    reviewer_id BIGINT NOT NULL,
    reviewee_id BIGINT NOT NULL,
    rating VARCHAR(16) NOT NULL,
    content VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_review_order_reviewer UNIQUE (order_id, reviewer_id)
);
-- audit_logs
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT,
    username VARCHAR(32),
    module VARCHAR(32),
    action VARCHAR(64),
    resource_id VARCHAR(64),
    detail TEXT,
    ip VARCHAR(64),
    request_id VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- exchange_proposals 换物提案
CREATE TABLE IF NOT EXISTS exchange_proposals (
    id BIGSERIAL PRIMARY KEY,
    proposal_no VARCHAR(40) NOT NULL UNIQUE,
    offeror_id BIGINT NOT NULL,
    offeree_id BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    turn VARCHAR(16) NOT NULL DEFAULT 'offeree',
    round INTEGER NOT NULL DEFAULT 0,
    note TEXT,
    top_up_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    top_up_payer VARCHAR(16) NOT NULL DEFAULT 'offeror',
    accepted_at TIMESTAMPTZ,
    rejected_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    last_action_by BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_exchange_proposals_offeror ON exchange_proposals (offeror_id);
CREATE INDEX IF NOT EXISTS idx_exchange_proposals_offeree ON exchange_proposals (offeree_id);
CREATE INDEX IF NOT EXISTS idx_exchange_proposals_status ON exchange_proposals (status);
CREATE INDEX IF NOT EXISTS idx_exchange_proposals_expires_at ON exchange_proposals (expires_at);
-- exchange_proposal_items 提案物品快照（side: offer 发起方换出 / target 对方换入）
CREATE TABLE IF NOT EXISTS exchange_proposal_items (
    id BIGSERIAL PRIMARY KEY,
    proposal_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    owner_id BIGINT NOT NULL,
    side VARCHAR(16) NOT NULL,
    title VARCHAR(128) NOT NULL,
    price NUMERIC(12,2) NOT NULL DEFAULT 0,
    image VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exchange_item_proposal_side ON exchange_proposal_items (proposal_id, side);
-- exchange_proposal_histories 提案完整历史
CREATE TABLE IF NOT EXISTS exchange_proposal_histories (
    id BIGSERIAL PRIMARY KEY,
    proposal_id BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    actor_role VARCHAR(16) NOT NULL,
    action VARCHAR(32) NOT NULL,
    note TEXT,
    top_up_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    top_up_payer VARCHAR(16) NOT NULL DEFAULT 'offeror',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exchange_history_proposal ON exchange_proposal_histories (proposal_id);
