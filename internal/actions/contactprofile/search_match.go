//go:build server

package contactprofile

import (
	"github.com/uptrace/bun"
)

// ApplyNameMatch 为已限定工作区的联系人查询追加名称检索条件：DENORM/档案名、联系方式、渠道身份名或联系人编号匹配。
// contact 是联系人表别名；pattern 是已转义的 ILIKE 模式；number 为解析出的编号，nil 时不匹配任何编号。
func ApplyNameMatch(query *bun.SelectQuery, contact, pattern string, number *int64) *bun.SelectQuery {
	return query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
		return group.
			Where("COALESCE("+ResolvedColumn(contact)+", "+contact+".display_name, '') ILIKE ?", pattern).
			WhereOr("EXISTS (SELECT 1 FROM contact_methods AS cm WHERE cm.workspace_id = "+contact+".workspace_id AND cm.contact_id = "+contact+".id AND cm.normalized_value ILIKE ?)", pattern).
			WhereOr("EXISTS (SELECT 1 FROM channel_identities AS name_cci WHERE name_cci.workspace_id = "+contact+".workspace_id AND name_cci.contact_id = "+contact+".id AND COALESCE(name_cci.display_name, '') ILIKE ?)", pattern).
			WhereOr(contact+".number = ?", number)
	})
}
