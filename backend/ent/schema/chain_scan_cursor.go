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

type ChainScanCursor struct{ ent.Schema }

func (ChainScanCursor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "chain_scan_cursors"}}
}

func (ChainScanCursor) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (ChainScanCursor) Fields() []ent.Field {
	return []ent.Field{
		field.String("network").MaxLen(32).Immutable(),
		field.Int64("chain_id").NonNegative().Immutable(),
		field.Int64("finalized_height").NonNegative().Default(0),
		field.String("finalized_hash").MaxLen(128).Default(""),
		field.String("lease_owner").MaxLen(128).Optional().Nillable(),
		field.Time("lease_until").Optional().Nillable(),
		field.String("health").MaxLen(32).Default("PAUSED"),
		field.Time("last_success_at").Optional().Nillable(),
		field.String("last_error_code").MaxLen(128).Optional().Nillable(),
		field.String("last_error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int("version").NonNegative().Default(0),
	}
}

func (ChainScanCursor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("network").Unique(),
		index.Fields("lease_until"),
		index.Fields("health", "updated_at"),
	}
}
