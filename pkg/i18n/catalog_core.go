package i18n

func init() {
	Register(ZhCN, map[string]string{
		"common.confirm": "确认",
		"common.cancel":  "取消",
		"common.close":   "关闭",
		"common.save":    "保存",
		"common.delete":  "删除",
		"common.edit":    "编辑",
		"common.create":  "新建",
		"common.open":    "打开",
	})

	Register(EnUS, map[string]string{
		"common.confirm": "Confirm",
		"common.cancel":  "Cancel",
		"common.close":   "Close",
		"common.save":    "Save",
		"common.delete":  "Delete",
		"common.edit":    "Edit",
		"common.create":  "Create",
		"common.open":    "Open",
	})
}
