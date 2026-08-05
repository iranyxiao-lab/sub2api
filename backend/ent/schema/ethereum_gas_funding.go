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

type EthereumGasFunding struct{ ent.Schema }

func (EthereumGasFunding) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ethereum_gas_fundings"}}
}

func (EthereumGasFunding) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (EthereumGasFunding) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("intent_id").Positive().Immutable(),
		field.Int64("chain_id").Positive().Immutable(),
		field.String("sponsor_address").MaxLen(64).Immutable(),
		field.String("target_address").MaxLen(64).Immutable(),
		field.Int64("derivation_index").NonNegative().Immutable(),
		field.String("amount_wei").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Immutable(),
		field.String("idempotency_key").MaxLen(128).Immutable(),
		field.String("signer_request_digest").MaxLen(128).Optional().Nillable(),
		field.String("signer_audit_id").MaxLen(128).Optional().Nillable(),
		field.Int64("nonce").NonNegative(),
		field.Int64("gas_limit").Positive(),
		field.String("max_fee_per_gas_wei").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}),
		field.String("max_priority_fee_per_gas_wei").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}),
		field.String("transaction_hash").MaxLen(128).Optional().Nillable(),
		field.Int64("replacement_of_id").Positive().Optional().Nillable(),
		field.String("status").MaxLen(32).Default("PREPARED"),
		field.Bool("finalized").Default(false),
		field.Int64("finalized_block_height").NonNegative().Optional().Nillable(),
		field.String("finalized_block_hash").MaxLen(128).Optional().Nillable(),
		field.String("actual_fee_wei").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Optional().Nillable(),
		field.Int("retry_count").NonNegative().Default(0),
		field.String("failure_code").MaxLen(128).Optional().Nillable(),
		field.String("failure_reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("next_attempt_at").Optional().Nillable(),
		field.Time("finalized_at").Optional().Nillable(),
		field.Int("version").NonNegative().Default(0),
	}
}

func (EthereumGasFunding) Edges() []ent.Edge {
	return []ent.Edge{edge.From("intent", OnchainPaymentIntent.Type).Ref("ethereum_gas_fundings").Field("intent_id").Unique().Required().Immutable()}
}

func (EthereumGasFunding) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("idempotency_key").Unique(),
		index.Fields("chain_id", "sponsor_address", "nonce"),
		index.Fields("chain_id", "target_address", "status"),
		index.Fields("transaction_hash").Unique().Annotations(entsql.IndexWhere("transaction_hash IS NOT NULL AND transaction_hash <> ''")),
		index.Fields("status", "next_attempt_at"),
		index.Fields("replacement_of_id"),
	}
}
