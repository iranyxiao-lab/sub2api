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

type OnchainDeposit struct{ ent.Schema }

func (OnchainDeposit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "onchain_deposits"}}
}

func (OnchainDeposit) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (OnchainDeposit) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("intent_id").Positive().Immutable(),
		field.Int64("payment_order_id").Positive().Immutable(),
		field.Int64("user_id").Positive().Immutable(),
		field.String("network").MaxLen(32).Immutable(),
		field.Int64("chain_id").NonNegative().Immutable(),
		field.String("transaction_id").MaxLen(128).Immutable(),
		field.Int64("log_index").NonNegative().Immutable(),
		field.Int64("transaction_index").NonNegative().Default(0).Immutable(),
		field.Int64("block_height").NonNegative().Immutable(),
		field.String("block_hash").MaxLen(128).Immutable(),
		field.String("token_contract").MaxLen(128).Immutable(),
		field.String("from_address").MaxLen(128).Immutable(),
		field.String("to_address").MaxLen(128).Immutable(),
		field.String("amount_raw").SchemaType(map[string]string{dialect.Postgres: "numeric(78,0)"}).Immutable(),
		field.Time("transaction_time").Immutable(),
		field.Bool("receipt_success").Immutable(),
		field.Bool("finalized").Default(true).Immutable(),
		field.String("status").MaxLen(32).Default("CONFIRMED"),
		field.String("validation_error").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("credit_audit_ref").MaxLen(128).Optional().Nillable(),
		field.Time("credited_at").Optional().Nillable(),
		field.Int("version").NonNegative().Default(0),
	}
}

func (OnchainDeposit) Edges() []ent.Edge {
	return []ent.Edge{edge.From("intent", OnchainPaymentIntent.Type).Ref("deposits").Field("intent_id").Unique().Required().Immutable()}
}

func (OnchainDeposit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("network", "transaction_id", "log_index").Unique(),
		index.Fields("intent_id", "status"),
		index.Fields("payment_order_id"),
		index.Fields("user_id", "transaction_time"),
		index.Fields("network", "block_height", "transaction_index", "log_index"),
		index.Fields("network", "to_address"),
		index.Fields("status", "created_at"),
	}
}
