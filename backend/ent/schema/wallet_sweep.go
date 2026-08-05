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

type WalletSweep struct{ ent.Schema }

func (WalletSweep) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "wallet_sweeps"}}
}

func (WalletSweep) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (WalletSweep) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("intent_id").Positive().Immutable(),
		field.String("network").MaxLen(32).Immutable(),
		field.Int64("chain_id").NonNegative().Immutable(),
		field.String("source_address").MaxLen(128).Immutable(),
		field.String("destination_address").MaxLen(128).Immutable(),
		field.String("balance_snapshot_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Immutable(),
		field.String("amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Immutable(),
		field.String("idempotency_key").MaxLen(128).Immutable(),
		field.String("signer_request_digest").MaxLen(128).Optional().Nillable(),
		field.String("signer_audit_id").MaxLen(128).Optional().Nillable(),
		field.String("transaction_id").MaxLen(128).Optional().Nillable(),
		field.Int64("nonce").NonNegative().Optional().Nillable(),
		field.Int64("replacement_of_id").Positive().Optional().Nillable(),
		field.String("fee_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Default("0"),
		field.Int64("energy_used").NonNegative().Default(0),
		field.Int64("bandwidth_used").NonNegative().Default(0),
		field.Int64("finalized_block_height").NonNegative().Optional().Nillable(),
		field.String("finalized_block_hash").MaxLen(128).Optional().Nillable(),
		field.String("status").MaxLen(32).Default("PREPARED"),
		field.Int("retry_count").NonNegative().Default(0),
		field.String("failure_code").MaxLen(128).Optional().Nillable(),
		field.String("failure_reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("next_attempt_at").Optional().Nillable(),
		field.Time("finalized_at").Optional().Nillable(),
		field.Int("version").NonNegative().Default(0),
	}
}

func (WalletSweep) Edges() []ent.Edge {
	return []ent.Edge{edge.From("intent", OnchainPaymentIntent.Type).Ref("wallet_sweeps").Field("intent_id").Unique().Required().Immutable()}
}

func (WalletSweep) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("idempotency_key").Unique(),
		index.Fields("network", "source_address", "status"),
		index.Fields("network", "transaction_id").Unique().Annotations(entsql.IndexWhere("transaction_id IS NOT NULL AND transaction_id <> ''")),
		index.Fields("status", "next_attempt_at"),
		index.Fields("replacement_of_id"),
	}
}
