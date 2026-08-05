package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OnchainPaymentIntent struct{ ent.Schema }

func (OnchainPaymentIntent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "onchain_payment_intents"}}
}

func (OnchainPaymentIntent) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (OnchainPaymentIntent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("payment_order_id").Positive().Immutable(),
		field.Int64("user_id").Positive().Immutable(),
		field.String("network").MaxLen(32).Immutable(),
		field.Int64("chain_id").NonNegative().Immutable(),
		field.String("token_contract").MaxLen(128).Immutable(),
		field.String("deposit_address").MaxLen(128).Immutable(),
		field.Int64("derivation_index").NonNegative().Immutable(),
		field.String("expected_amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Immutable(),
		field.String("received_amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Default("0"),
		field.String("credited_amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Default("0"),
		field.String("overpaid_amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Default("0"),
		field.JSON("config_snapshot", map[string]any{}).SchemaType(map[string]string{dialect.Postgres: "jsonb"}).Immutable(),
		field.String("config_version").MaxLen(64).Immutable(),
		field.String("status").MaxLen(32).Default("PENDING"),
		field.String("settlement_idempotency_key").MaxLen(128).Optional().Nillable(),
		field.Int("settlement_attempts").NonNegative().Default(0),
		field.Time("next_settlement_at").Optional().Nillable(),
		field.String("last_error_code").MaxLen(128).Optional().Nillable(),
		field.String("last_error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("settled_at").Optional().Nillable(),
		field.Int("version").NonNegative().Default(0),
	}
}

func (OnchainPaymentIntent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("payment_order", PaymentOrder.Type).Ref("onchain_payment_intent").Field("payment_order_id").Unique().Required().Immutable(),
		edge.To("deposits", OnchainDeposit.Type),
		edge.To("wallet_sweeps", WalletSweep.Type),
		edge.To("ethereum_gas_fundings", EthereumGasFunding.Type),
	}
}

func (OnchainPaymentIntent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("payment_order_id").Unique(),
		index.Fields("network", "derivation_index").Unique(),
		index.Fields("network", "deposit_address").Unique(),
		index.Fields("settlement_idempotency_key").Unique().Annotations(entsql.IndexWhere("settlement_idempotency_key IS NOT NULL AND settlement_idempotency_key <> ''")),
		index.Fields("user_id", "status"),
		index.Fields("status", "next_settlement_at"),
	}
}
