package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type EthereumNonceState struct{ ent.Schema }

func (EthereumNonceState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ethereum_nonce_states"}}
}

func (EthereumNonceState) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (EthereumNonceState) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("chain_id").Positive().Immutable(),
		field.String("sender_address").MaxLen(64).Immutable(),
		field.Int64("next_nonce").NonNegative().Default(0),
		field.Int64("observed_pending_nonce").NonNegative().Default(0),
		field.String("status").MaxLen(32).Default("RECONCILING"),
		field.String("lease_owner").MaxLen(128).Optional().Nillable(),
		field.Time("lease_until").Optional().Nillable(),
		field.Time("last_reconciled_at").Optional().Nillable(),
		field.String("last_error_code").MaxLen(128).Optional().Nillable(),
		field.String("last_error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int("version").NonNegative().Default(0),
	}
}

func (EthereumNonceState) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("chain_id", "sender_address").Unique(),
		index.Fields("status", "lease_until"),
	}
}
