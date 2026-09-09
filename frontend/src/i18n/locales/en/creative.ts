export default {
  creative: {
    title: 'Creative Studio',
    description: 'Generate and edit images with AI models; assets stay in this browser only.',
    panel: {
      model: 'Model',
      // Edit / inpaint prerequisite hint (also shown as the form error when generating without a selection)
      selectImageHint: 'Select an image on the canvas before generating.',
      // Inpaint hint: select an image and paint the area to redraw with the brush
      maskHint: 'Select an image on the canvas first, then paint the area to redraw with the brush.',
      imageSize: 'Image size',
      aspectRatio: 'Aspect ratio',
      quality: 'Quality',
      background: 'Background',
      thinkingLevel: 'Thinking level',
      estimatedCost: '{cost} / image',
      uploadSource: 'Upload image',
      promptPlaceholder: 'Describe the image you want to create...',
      // 模型目录为空时的空态提示：区分功能被管理员关闭与分组未配置图片生成。
      studioDisabled: 'Creative Studio is disabled. Please contact your administrator to enable it.',
      noModelsAvailable: 'No image generation models are available. Please ask your administrator to configure a group with image generation enabled.',
    },
    composer: {
      send: 'Send',
      model: 'Model',
      params: 'Params',
      operation: 'Operation',
      selectModel: 'Select a model',
      selectModelFirst: 'Select a model first.',
    },
    operations: {
      generate: 'Generate',
      edit: 'Edit',
      inpaint: 'Inpaint',
    },
    operationsDesc: {
      generate: 'Generate an image from the prompt alone',
      edit: 'Edit using the selected canvas image as reference',
      inpaint: 'Paint over the selected image to redraw that area',
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
      low: 'Low',
      medium: 'Medium',
      high: 'High',
      auto: 'Auto',
    },
    backgrounds: {
      auto: 'Auto',
      opaque: 'Opaque',
      transparent: 'Transparent',
    },
    thinkingLevels: {
      minimal: 'Minimal',
      high: 'High',
    },
    canvas: {
      brushSize: 'Brush size',
      shapeRound: 'Round stroke',
      shapeSquare: 'Square stroke',
      // 框选画布对象：三种创作模式均可开启
      boxSelect: 'Select by dragging a box',
      // 涂抹开关：暂停涂抹后可平移视角 / 换选图片，再点恢复涂抹
      paintToggleOn: 'Start painting',
      paintToggleOff: 'Pause painting to pan',
      // 撤销上一笔涂抹（同 Ctrl/Cmd+Z）
      undoMask: 'Undo last stroke',
      clearMask: 'Clear all paint',
      downloadSelected: 'Download selected image',
      removeSelected: 'Remove selected image',
      reset: 'Clear canvas',
      backToDashboard: 'Back to dashboard',
      settings: 'Settings',
      // 局部重绘未选中图片时的引导
      inpaintPickHint: 'Click an image on the canvas, then paint the area to redraw',
      // 图生图未选择参考图时的引导（点击单选，或直接拖拽框选多张）
      editPickHint: 'Tap an image as reference, or drag a box to select multiple images',
      // 涂抹开始前的引导：紫色笔迹即重绘区域
      maskPaintHint: 'Paint the purple area to redraw; it becomes the mask on export',
    },
    result: {
      actualCost: 'Actual cost: {cost}',
      missing: 'Missing',
    },
    history: {
      title: 'History',
      elapsed: 'Elapsed {time}',
      toggle: 'Toggle run history',
      empty: 'No creative runs yet.',
      importToCanvas: 'Send to canvas',
      download: 'Download',
      clearData: 'Clear local creative data',
      clearSuccess: 'Local creative data cleared.',
      confirmClearTitle: 'Clear local creative data?',
      confirmClearMessage:
        'This permanently deletes locally stored source images, masks, results, canvas scenes and creative drafts, and resets this browser workspace. Older history will be hidden from this browser (task metadata remains on the server), and new runs will use a new workspace. Images already generated cannot be recovered. This does not affect your account balance.',
    },
    status: {
      queued: 'Queued',
      running: 'Running',
      provider_succeeded: 'Awaiting settlement',
      settlement_pending: 'Settling',
      release_pending: 'Releasing hold',
      succeeded: 'Succeeded',
      failed: 'Failed',
      cancelled: 'Cancelled',
      result_lost: 'Result lost',
      submitting: 'Submitting',
    },
    cropper: {
      title: 'Crop image',
      hint: 'Drag to adjust the crop area. The cropped image is stored only in this browser.',
      confirm: 'Apply crop',
      skip: 'Skip cropping',
    },
    error: {
      loadModelsFailed: 'Failed to load creative models. Please try again later.',
      noModel: 'Please select a model first.',
      operationNotSupported: 'The selected model does not support this operation.',
      promptTooLong: 'Prompt exceeds the server limit.',
      assetTooLarge: 'The image exceeds the server size or format limit.',
      sourceRequired: 'Edit and inpaint operations require at least one source image.',
      maskRequired: 'Inpaint requires a mask. Select an image and paint the area with the brush first.',
      referenceLimit: 'This model supports at most {max} reference images.',
      submitFailed: 'Failed to submit the creative run. Please retry.',
      historyFailed: 'Failed to load run history. Check this browser storage permission and retry.',
      workspaceUnavailable: 'This browser workspace is unavailable. Allow local storage for this site and retry.',
      clearFailed: 'Failed to clear local data.',
      loadImageFailed: 'Failed to load the image.',
      quotaExceeded: 'Local storage is full. Please download your assets before continuing.',
      dropInvalid: 'The dropped history image could not be recognized.',
      dropUnsupported: 'Only PNG, JPEG, or WebP images are supported.',
      dropHistoryUnavailable: 'This history image is no longer available in this browser.',
    },
  },
}
