package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type WalletDerivationCursor struct{ ent.Schema }

func (WalletDerivationCursor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "wallet_derivation_cursors"}}
}

func (WalletDerivationCursor) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }

func (WalletDerivationCursor) Fields() []ent.Field {
	return []ent.Field{
		field.String("network").MaxLen(32).Immutable(),
		field.Int64("next_index").NonNegative().Default(0),
		field.Int("version").NonNegative().Default(0),
	}
}

func (WalletDerivationCursor) Indexes() []ent.Index {
	return []ent.Index{index.Fields("network").Unique()}
}
