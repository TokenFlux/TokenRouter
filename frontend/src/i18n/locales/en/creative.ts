// creative.ts supplies English text for image creation, editing, canvas
// controls, and run history. Prompts, selection guidance, generation parameters,
// and local-data messages describe the creative workspace states so its controls
// and validation errors share consistent wording.

export default {
  creative: {
    title: 'Creative Studio',
    description: 'Generate and edit images with AI models; assets stay in this browser only.',
    panel: {
      outputFormat: 'Output format',
      frameCount: 'Frame count',
      frameDuration: 'Frame delay (ms)',
      model: 'Model',
      // Image selection guidance shared with generation validation when no reference image is selected.
      selectImageHint: 'Select an image on the canvas before generating.',
      // Inpaint guidance describes selecting an image and painting its redraw area.
      maskHint:
        'Select an image on the canvas first, then paint the area to redraw with the brush.',
      imageSize: 'Image size',
      aspectRatio: 'Aspect ratio',
      quality: 'Quality',
      background: 'Background',
      thinkingLevel: 'Thinking level',
      estimatedCost: '{cost} / image',
      uploadSource: 'Upload image',
      promptPlaceholder: 'Describe the image you want to create...',
      // Separate empty states describe an administrator-disabled studio and groups lacking image generation.
      studioDisabled:
        'Creative Studio is disabled. Please contact your administrator to enable it.',
      noModelsAvailable:
        'No image generation models are available. Please ask your administrator to configure a group with image generation enabled.',
    },
    composer: {
      send: 'Send',
      model: 'Model',
      params: 'Params',
      operation: 'Operation',
      selectModel: 'Select a model',
      selectModelFirst: 'Select a model first.',
      autoRatio: 'Auto ratio',
    },
    empty: {
      title: 'Start with a description',
      description: 'Type a prompt below to generate an image, or upload one to edit or inpaint it.',
      upload: 'Upload image',
      examples: {
        poster: {
          label: 'Retro travel poster',
          prompt:
            'A 1970s retro travel poster of a seaside town at sunset, warm orange tones, grainy paper texture, large sans-serif title',
        },
        product: {
          label: 'Product shot',
          prompt:
            'Studio product shot of a frosted glass perfume bottle on a light gray background, soft side lighting, subtle reflections, simple composition',
        },
        illustration: {
          label: 'Flat illustration',
          prompt:
            'An orange cat reading a book by the window, flat illustration style, muted colors, indoor plants, afternoon sunlight',
        },
      },
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
    // Quality labels correspond to the generation quality values exposed by model settings.
    qualities: {
      xhigh: 'Extra high',
      max: 'Maximum',
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
      playGIF: 'Play GIF',
      stopGIF: 'Stop GIF',
      gifReadOnly: 'GIF outputs can be previewed and downloaded. Use a still image for editing.',
      brushSize: 'Brush size',
      shapeRound: 'Round stroke',
      shapeSquare: 'Square stroke',
      // Box selection labels identify selection of canvas objects.
      boxSelect: 'Select by dragging a box',
      // Painting controls distinguish drawing from pausing to pan or select another image.
      paintToggleOn: 'Start painting',
      paintToggleOff: 'Pause painting to pan',
      // Undo reverses the newest retained mask operation, including clearing
      // all strokes.
      undoMask: 'Undo last stroke',
      clearMask: 'Clear all paint',
      downloadSelected: 'Download selected image',
      removeSelected: 'Remove selected image',
      reset: 'Clear canvas',
      backToDashboard: 'Back to dashboard',
      settings: 'Settings',
      // Inpaint selection guidance appears before an image is selected.
      inpaintPickHint: 'Click an image on the canvas, then paint the area to redraw',
      // Reference selection guidance describes clicking an image or selecting several with a box.
      editPickHint: 'Tap an image as reference, or drag a box to select multiple images',
      // Mask guidance identifies the painted area sent for redrawing.
      maskPaintHint: 'Paint over the area to redraw. The purple strokes are sent as the mask.',
    },
    result: {
      actualCost: 'Actual cost: {cost}',
      missing: 'Missing',
    },
    history: {
      noOutputs: 'No output images',
      title: 'History',
      elapsed: 'Elapsed {time}',
      toggle: 'Toggle run history',
      empty: 'No creative runs yet.',
      importToCanvas: 'Send to canvas',
      download: 'Download',
      copyPrompt: 'Copy prompt',
      promptCopied: 'Prompt copied',
      reuseParams: 'Use in composer',
      // Prompt availability refers to history stored by the submitting browser.
      promptUnavailable: 'This browser has no prompt saved for this run.',
      // Short parameter labels fit the history sidebar.
      params: {
        group: 'Group',
        size: 'Size',
        ratio: 'Ratio',
        thinking: 'Thinking',
        references: 'References',
        count: 'Images',
      },
      clearData: 'Clear local creative data',
      clearSuccess: 'Local creative data cleared.',
      confirmClearTitle: 'Clear local creative data?',
      confirmClearMessage:
        'This permanently deletes locally stored source images, masks, results, canvas scenes, creative drafts and run prompts, and resets this browser workspace. Older history will be hidden from this browser (task metadata remains on the server), and new runs will use a new workspace. Images already generated cannot be recovered. Your account balance stays the same.',
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
      playGIFFailed: 'Could not play this GIF. Please try again.',
      loadModelsFailed: 'Failed to load creative models. Please try again later.',
      noModel: 'Please select a model first.',
      operationNotSupported: 'The selected model does not support this operation.',
      promptTooLong: 'Prompt exceeds the server limit.',
      assetTooLarge: 'The image exceeds the server size or format limit.',
      sourceRequired: 'Edit and inpaint operations require at least one source image.',
      maskRequired:
        'Inpaint requires a mask. Select an image and paint the area with the brush first.',
      referenceLimit: 'This model supports at most {max} reference images.',
      submitFailed: 'Failed to submit the creative run. Please retry.',
      historyFailed: 'Failed to load run history. Check this browser storage permission and retry.',
      workspaceUnavailable:
        'This browser workspace is unavailable. Allow local storage for this site and retry.',
      clearFailed: 'Failed to clear local data.',
      loadImageFailed: 'Failed to load the image.',
      quotaExceeded: 'Local storage is full. Please download your assets before continuing.',
      dropInvalid: 'The dropped history image could not be recognized.',
      dropUnsupported: 'Only PNG, JPEG, or WebP images are supported.',
      dropHistoryUnavailable: 'This history image is no longer available in this browser.',
    },
  },
}
