-- Durable on-chain USDT payment, scanning, gas-funding, and sweep state.
-- The existing payment_orders row remains the business-order source of truth;
-- this migration only references it from the new exact-integer ledger.

CREATE TABLE IF NOT EXISTS chain_scan_cursors (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    network VARCHAR(32) NOT NULL,
    chain_id BIGINT NOT NULL CHECK (chain_id >= 0),
    finalized_height BIGINT NOT NULL DEFAULT 0 CHECK (finalized_height >= 0),
    finalized_hash VARCHAR(128) NOT NULL DEFAULT '',
    lease_owner VARCHAR(128),
    lease_until TIMESTAMPTZ,
    health VARCHAR(32) NOT NULL DEFAULT 'PAUSED',
    last_success_at TIMESTAMPTZ,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS chainscancursor_network
    ON chain_scan_cursors (network);
CREATE INDEX IF NOT EXISTS chainscancursor_lease_until
    ON chain_scan_cursors (lease_until);
CREATE INDEX IF NOT EXISTS chainscancursor_health_updated_at
    ON chain_scan_cursors (health, updated_at);

CREATE TABLE IF NOT EXISTS wallet_derivation_cursors (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    network VARCHAR(32) NOT NULL,
    next_index BIGINT NOT NULL DEFAULT 0 CHECK (next_index >= 0),
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS walletderivationcursor_network
    ON wallet_derivation_cursors (network);

CREATE TABLE IF NOT EXISTS ethereum_nonce_states (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    sender_address VARCHAR(64) NOT NULL,
    next_nonce BIGINT NOT NULL DEFAULT 0 CHECK (next_nonce >= 0),
    observed_pending_nonce BIGINT NOT NULL DEFAULT 0 CHECK (observed_pending_nonce >= 0),
    status VARCHAR(32) NOT NULL DEFAULT 'RECONCILING',
    lease_owner VARCHAR(128),
    lease_until TIMESTAMPTZ,
    last_reconciled_at TIMESTAMPTZ,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS ethereumnoncestate_chain_id_sender_address
    ON ethereum_nonce_states (chain_id, sender_address);
CREATE INDEX IF NOT EXISTS ethereumnoncestate_status_lease_until
    ON ethereum_nonce_states (status, lease_until);

CREATE TABLE IF NOT EXISTS onchain_payment_intents (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    network VARCHAR(32) NOT NULL,
    chain_id BIGINT NOT NULL CHECK (chain_id >= 0),
    token_contract VARCHAR(128) NOT NULL,
    deposit_address VARCHAR(128) NOT NULL,
    derivation_index BIGINT NOT NULL CHECK (derivation_index >= 0),
    expected_amount_raw NUMERIC(78,0) NOT NULL CHECK (expected_amount_raw > 0),
    received_amount_raw NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (received_amount_raw >= 0),
    credited_amount_raw NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (credited_amount_raw >= 0),
    overpaid_amount_raw NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (overpaid_amount_raw >= 0),
    config_snapshot JSONB NOT NULL,
    config_version VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    settlement_idempotency_key VARCHAR(128),
    settlement_attempts INTEGER NOT NULL DEFAULT 0 CHECK (settlement_attempts >= 0),
    next_settlement_at TIMESTAMPTZ,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    settled_at TIMESTAMPTZ,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    payment_order_id BIGINT NOT NULL,
    CONSTRAINT onchain_payment_intents_payment_orders_onchain_payment_intent
        FOREIGN KEY (payment_order_id) REFERENCES payment_orders (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS onchainpaymentintent_payment_order_id
    ON onchain_payment_intents (payment_order_id);
CREATE UNIQUE INDEX IF NOT EXISTS onchainpaymentintent_network_derivation_index
    ON onchain_payment_intents (network, derivation_index);
CREATE UNIQUE INDEX IF NOT EXISTS onchainpaymentintent_network_deposit_address
    ON onchain_payment_intents (network, deposit_address);
CREATE UNIQUE INDEX IF NOT EXISTS onchainpaymentintent_settlement_idempotency_key
    ON onchain_payment_intents (settlement_idempotency_key)
    WHERE settlement_idempotency_key IS NOT NULL AND settlement_idempotency_key <> '';
CREATE INDEX IF NOT EXISTS onchainpaymentintent_user_id_status
    ON onchain_payment_intents (user_id, status);
CREATE INDEX IF NOT EXISTS onchainpaymentintent_status_next_settlement_at
    ON onchain_payment_intents (status, next_settlement_at);

CREATE TABLE IF NOT EXISTS onchain_deposits (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    payment_order_id BIGINT NOT NULL CHECK (payment_order_id > 0),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    network VARCHAR(32) NOT NULL,
    chain_id BIGINT NOT NULL CHECK (chain_id >= 0),
    transaction_id VARCHAR(128) NOT NULL,
    log_index BIGINT NOT NULL CHECK (log_index >= 0),
    transaction_index BIGINT NOT NULL DEFAULT 0 CHECK (transaction_index >= 0),
    block_height BIGINT NOT NULL CHECK (block_height >= 0),
    block_hash VARCHAR(128) NOT NULL,
    token_contract VARCHAR(128) NOT NULL,
    from_address VARCHAR(128) NOT NULL,
    to_address VARCHAR(128) NOT NULL,
    amount_raw NUMERIC(78,0) NOT NULL CHECK (amount_raw > 0),
    transaction_time TIMESTAMPTZ NOT NULL,
    receipt_success BOOLEAN NOT NULL,
    finalized BOOLEAN NOT NULL DEFAULT TRUE,
    status VARCHAR(32) NOT NULL DEFAULT 'CONFIRMED',
    validation_error TEXT,
    credit_audit_ref VARCHAR(128),
    credited_at TIMESTAMPTZ,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    intent_id BIGINT NOT NULL,
    CONSTRAINT onchain_deposits_onchain_payment_intents_deposits
        FOREIGN KEY (intent_id) REFERENCES onchain_payment_intents (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS onchaindeposit_network_transaction_id_log_index
    ON onchain_deposits (network, transaction_id, log_index);
CREATE INDEX IF NOT EXISTS onchaindeposit_intent_id_status
    ON onchain_deposits (intent_id, status);
CREATE INDEX IF NOT EXISTS onchaindeposit_payment_order_id
    ON onchain_deposits (payment_order_id);
CREATE INDEX IF NOT EXISTS onchaindeposit_user_id_transaction_time
    ON onchain_deposits (user_id, transaction_time);
CREATE INDEX IF NOT EXISTS onchaindeposit_network_block_height_transaction_index_log_index
    ON onchain_deposits (network, block_height, transaction_index, log_index);
CREATE INDEX IF NOT EXISTS onchaindeposit_network_to_address
    ON onchain_deposits (network, to_address);
CREATE INDEX IF NOT EXISTS onchaindeposit_status_created_at
    ON onchain_deposits (status, created_at);

CREATE TABLE IF NOT EXISTS wallet_sweeps (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    network VARCHAR(32) NOT NULL,
    chain_id BIGINT NOT NULL CHECK (chain_id >= 0),
    source_address VARCHAR(128) NOT NULL,
    destination_address VARCHAR(128) NOT NULL,
    balance_snapshot_raw NUMERIC(78,0) NOT NULL CHECK (balance_snapshot_raw >= 0),
    amount_raw NUMERIC(78,0) NOT NULL CHECK (amount_raw > 0),
    idempotency_key VARCHAR(128) NOT NULL,
    signer_request_digest VARCHAR(128),
    signer_audit_id VARCHAR(128),
    transaction_id VARCHAR(128),
    nonce BIGINT CHECK (nonce >= 0),
    replacement_of_id BIGINT CHECK (replacement_of_id > 0),
    fee_raw NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (fee_raw >= 0),
    energy_used BIGINT NOT NULL DEFAULT 0 CHECK (energy_used >= 0),
    bandwidth_used BIGINT NOT NULL DEFAULT 0 CHECK (bandwidth_used >= 0),
    finalized_block_height BIGINT CHECK (finalized_block_height >= 0),
    finalized_block_hash VARCHAR(128),
    status VARCHAR(32) NOT NULL DEFAULT 'PREPARED',
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    failure_code VARCHAR(128),
    failure_reason TEXT,
    next_attempt_at TIMESTAMPTZ,
    finalized_at TIMESTAMPTZ,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    intent_id BIGINT NOT NULL,
    CONSTRAINT wallet_sweeps_onchain_payment_intents_wallet_sweeps
        FOREIGN KEY (intent_id) REFERENCES onchain_payment_intents (id) ON DELETE RESTRICT,
    CONSTRAINT wallet_sweeps_replacement_of
        FOREIGN KEY (replacement_of_id) REFERENCES wallet_sweeps (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS walletsweep_idempotency_key
    ON wallet_sweeps (idempotency_key);
CREATE INDEX IF NOT EXISTS walletsweep_network_source_address_status
    ON wallet_sweeps (network, source_address, status);
CREATE UNIQUE INDEX IF NOT EXISTS walletsweep_network_transaction_id
    ON wallet_sweeps (network, transaction_id)
    WHERE transaction_id IS NOT NULL AND transaction_id <> '';
CREATE INDEX IF NOT EXISTS walletsweep_status_next_attempt_at
    ON wallet_sweeps (status, next_attempt_at);
CREATE INDEX IF NOT EXISTS walletsweep_replacement_of_id
    ON wallet_sweeps (replacement_of_id);

CREATE TABLE IF NOT EXISTS ethereum_gas_fundings (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    sponsor_address VARCHAR(64) NOT NULL,
    target_address VARCHAR(64) NOT NULL,
    derivation_index BIGINT NOT NULL CHECK (derivation_index >= 0),
    amount_wei NUMERIC(78,0) NOT NULL CHECK (amount_wei > 0),
    idempotency_key VARCHAR(128) NOT NULL,
    signer_request_digest VARCHAR(128),
    signer_audit_id VARCHAR(128),
    nonce BIGINT NOT NULL CHECK (nonce >= 0),
    gas_limit BIGINT NOT NULL CHECK (gas_limit > 0),
    max_fee_per_gas_wei NUMERIC(78,0) NOT NULL CHECK (max_fee_per_gas_wei > 0),
    max_priority_fee_per_gas_wei NUMERIC(78,0) NOT NULL CHECK (max_priority_fee_per_gas_wei >= 0),
    transaction_hash VARCHAR(128),
    replacement_of_id BIGINT CHECK (replacement_of_id > 0),
    status VARCHAR(32) NOT NULL DEFAULT 'PREPARED',
    finalized BOOLEAN NOT NULL DEFAULT FALSE,
    finalized_block_height BIGINT CHECK (finalized_block_height >= 0),
    finalized_block_hash VARCHAR(128),
    actual_fee_wei NUMERIC(78,0) CHECK (actual_fee_wei >= 0),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    failure_code VARCHAR(128),
    failure_reason TEXT,
    next_attempt_at TIMESTAMPTZ,
    finalized_at TIMESTAMPTZ,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    intent_id BIGINT NOT NULL,
    CONSTRAINT ethereum_gas_fundings_onchain_payment_intents_ethereum_gas_fundings
        FOREIGN KEY (intent_id) REFERENCES onchain_payment_intents (id) ON DELETE RESTRICT,
    CONSTRAINT ethereum_gas_fundings_replacement_of
        FOREIGN KEY (replacement_of_id) REFERENCES ethereum_gas_fundings (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS ethereumgasfunding_idempotency_key
    ON ethereum_gas_fundings (idempotency_key);
CREATE INDEX IF NOT EXISTS ethereumgasfunding_chain_id_sponsor_address_nonce
    ON ethereum_gas_fundings (chain_id, sponsor_address, nonce);
CREATE INDEX IF NOT EXISTS ethereumgasfunding_chain_id_target_address_status
    ON ethereum_gas_fundings (chain_id, target_address, status);
CREATE UNIQUE INDEX IF NOT EXISTS ethereumgasfunding_transaction_hash
    ON ethereum_gas_fundings (transaction_hash)
    WHERE transaction_hash IS NOT NULL AND transaction_hash <> '';
CREATE INDEX IF NOT EXISTS ethereumgasfunding_status_next_attempt_at
    ON ethereum_gas_fundings (status, next_attempt_at);
CREATE INDEX IF NOT EXISTS ethereumgasfunding_replacement_of_id
    ON ethereum_gas_fundings (replacement_of_id);
