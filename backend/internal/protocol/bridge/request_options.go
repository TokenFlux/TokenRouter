package bridge

// RequestOptions 是调用方选择的目标行为；转换器不从模型名推断平台策略。
type RequestOptions struct {
	DropSampling      bool
	SupportsMaxEffort bool
}
