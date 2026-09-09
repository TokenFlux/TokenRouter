export default {
  creative: {
    title: '创作台',
    description: '使用 AI 模型生成与编辑图片，素材仅保存在当前浏览器。',
    panel: {
      model: '模型',
      // 图生图 / 局部重绘前置条件提示（未选中图片时点击生成也会作为表单错误提示）
      selectImageHint: '请先在画布中选中一张图片，再开始生成。',
      // 局部重绘提示：选中图片后用画笔涂抹要重绘的区域
      maskHint: '请先在画布中选中一张图片，然后用画笔涂抹要重绘的区域。',
      imageSize: '图片尺寸',
      aspectRatio: '画面比例',
      quality: '画质',
      background: '背景',
      thinkingLevel: '思考强度',
      estimatedCost: '{cost} / 张',
      uploadSource: '上传图片',
      promptPlaceholder: '描述想要生成的画面...',
      // 模型目录为空时的空态提示：区分功能被管理员关闭与分组未配置图片生成。
      studioDisabled: '创作台功能已关闭，请联系管理员开启。',
      noModelsAvailable: '暂无可用的图片生成模型，请联系管理员配置支持图片生成的分组。',
    },
    composer: {
      send: '发送',
      model: '模型',
      params: '参数',
      operation: '操作',
      selectModel: '选择模型',
      selectModelFirst: '请先选择模型。',
    },
    operations: {
      generate: '文生图',
      edit: '图生图',
      inpaint: '局部重绘',
    },
    operationsDesc: {
      generate: '直接根据提示词生成图片',
      edit: '以画布中选中的图片为参考进行编辑',
      inpaint: '涂抹选中图片的区域并重新绘制',
    },
    aspects: {
      '1x1': '1:1',
      '4x3': '4:3',
      '3x4': '3:4',
      '16x9': '16:9',
      '9x16': '9:16',
    },
    // 生图画质档位由模型目录下发；支持时默认选择 medium，始终保持一个选项。
    qualities: {
      low: '低',
      medium: '中',
      high: '高',
      auto: '自动',
    },
    backgrounds: {
      auto: '自动',
      opaque: '不透明',
      transparent: '透明',
    },
    thinkingLevels: {
      minimal: '最少',
      high: '高',
    },
    canvas: {
      brushSize: '画笔粗细',
      shapeRound: '圆头笔迹',
      shapeSquare: '方头笔迹',
      // 框选画布对象：三种创作模式均可开启
      boxSelect: '框选画布对象',
      // 涂抹开关：暂停涂抹后可平移视角 / 换选图片，再点恢复涂抹
      paintToggleOn: '开始涂抹',
      paintToggleOff: '暂停涂抹，移动视角',
      // 撤销上一笔涂抹（同 Ctrl/Cmd+Z）
      undoMask: '撤销上一笔',
      clearMask: '清除全部涂抹',
      downloadSelected: '下载选中图片',
      removeSelected: '移除选中图片',
      reset: '清空画布',
      backToDashboard: '返回仪表盘',
      settings: '设置',
      // 局部重绘未选中图片时的引导
      inpaintPickHint: '点击画布中的一张图片，在其上涂抹要重绘的区域',
      // 图生图未选择参考图时的引导（点击单选，或直接拖拽框选多张）
      editPickHint: '点击图片选择参考图，或直接拖拽框选多张',
      // 涂抹开始前的引导：紫色笔迹即重绘区域
      maskPaintHint: '在图片上涂抹紫色区域，即要重绘的部分（导出时自动转为 mask）',
    },
    result: {
      actualCost: '实际费用：{cost}',
      missing: '素材缺失',
    },
    history: {
      title: '历史记录',
      elapsed: '已用时 {time}',
      toggle: '展开 / 收起历史记录',
      empty: '暂无创作记录。',
      importToCanvas: '导入到画布',
      download: '下载',
      clearData: '清空本机创作数据',
      clearSuccess: '本机创作数据已清空。',
      confirmClearTitle: '清空本机创作数据？',
      confirmClearMessage:
        '将永久删除本机保存的源图、mask、生成结果、画布场景与创作草稿，并重置当前浏览器工作区；旧历史会从本机列表隐藏（任务元数据仍保留在服务端），后续任务进入新的工作区。已生成的图片无法找回，且不会影响账户余额。',
    },
    status: {
      queued: '排队中',
      running: '生成中',
      provider_succeeded: '待结算',
      settlement_pending: '结算中',
      release_pending: '退款处理中',
      succeeded: '已成功',
      failed: '失败',
      cancelled: '已取消',
      result_lost: '结果丢失',
      submitting: '提交中',
    },
    cropper: {
      title: '裁剪图片',
      hint: '拖动调整裁剪区域，裁剪结果仅保存在当前浏览器。',
      confirm: '确认裁剪',
      skip: '跳过裁剪',
    },
    error: {
      loadModelsFailed: '模型目录加载失败，请稍后重试。',
      noModel: '请先选择模型。',
      operationNotSupported: '所选模型不支持该操作。',
      promptTooLong: '提示词超过服务端允许的长度。',
      assetTooLarge: '图片超过服务端允许的大小或格式限制。',
      sourceRequired: '图生图 / 局部重绘至少需要一张源图。',
      maskRequired: '局部重绘需要 mask，请先选中图片并用画笔绘制要重绘的区域。',
      referenceLimit: '当前模型最多支持 {max} 张参考图。',
      submitFailed: '提交生成失败，请重试。',
      historyFailed: '历史记录加载失败，请检查当前浏览器存储权限后重试。',
      workspaceUnavailable: '当前浏览器工作区不可用，请允许本站使用本地存储后重试。',
      clearFailed: '清空本机数据失败。',
      loadImageFailed: '图片加载失败。',
      quotaExceeded: '本地存储空间不足，请先下载备份素材。',
      dropInvalid: '无法识别拖入的历史图片。',
      dropUnsupported: '仅支持 PNG、JPEG 或 WebP 图片。',
      dropHistoryUnavailable: '历史图片已不在本机，无法拖入画布。',
    },
  },
}
