/** 原生端文件保存调用。 */
import { SaveTextFile } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

/** 在原生端打开保存对话框并写入文本文件，用户取消时返回 false。 */
export const saveNativeTextFile = bind(SaveTextFile)
