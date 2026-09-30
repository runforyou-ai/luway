/** 通讯录列表查询参数适配。 */
import { useListSearchParams } from "@/hooks/use-list-search-params"

/** 补充通讯录列表的选中项查询参数。 */
export function useContactSearch() {
  const listSearch = useListSearchParams({ resetParameter: "selected" })
  const selected = listSearch.searchParams.get("selected") ?? ""

  return {
    ...listSearch,
    selected,
  }
}
