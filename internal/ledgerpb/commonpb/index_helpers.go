package commonpb

func TxBuiltinIndexID(b TransactionBuiltinIndex) *IndexID {
	return &IndexID{Kind: &IndexID_TxBuiltin{TxBuiltin: b}}
}

func LogBuiltinIndexID(b LogBuiltinIndex) *IndexID {
	return &IndexID{Kind: &IndexID_LogBuiltin{LogBuiltin: b}}
}

func AccountBuiltinIndexID(b AccountBuiltinIndex) *IndexID {
	return &IndexID{Kind: &IndexID_AccountBuiltin{AccountBuiltin: b}}
}

func MetadataIndexIDFor(target TargetType, key string) *IndexID {
	return &IndexID{Kind: &IndexID_Metadata{Metadata: &MetadataIndexID{Target: target, Key: key}}}
}

func TxMetadataIndexID(key string) *IndexID {
	return MetadataIndexIDFor(TargetType_TARGET_TYPE_TRANSACTION, key)
}

func AccountMetadataIndexID(key string) *IndexID {
	return MetadataIndexIDFor(TargetType_TARGET_TYPE_ACCOUNT, key)
}
