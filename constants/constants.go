package constants

const (
	LogLevelFlag              = "log-level"
	HttpBindAddressServerFlag = "http-bind-address-server"
	HttpBindAddressWorkerFlag = "http-bind-address-worker"

	StorageMongoConnStringFlag   = "storage-mongo-conn-string"
	StorageMongoDatabaseNameFlag = "storage-mongo-database-name"

	KafkaBrokersFlag               = "kafka-brokers"
	KafkaGroupIDFlag               = "kafka-consumer-group"
	KafkaTopicsFlag                = "kafka-topics"
	KafkaTLSEnabledFlag            = "kafka-tls-enabled"
	KafkaTLSInsecureSkipVerifyFlag = "kafka-tls-insecure-skip-verify"
	KafkaSASLEnabledFlag           = "kafka-sasl-enabled"
	KafkaSASLMechanismFlag         = "kafka-sasl-mechanism"
	KafkaUsernameFlag              = "kafka-username"
	KafkaPasswordFlag              = "kafka-password"
)

const (
	DefaultBindAddressServer = ":8080"
	DefaultBindAddressWorker = ":8081"

	DefaultMongoConnString   = "mongodb://root:root@localhost:27017/?directConnection=true"
	DefaultMongoDatabaseName = "reconciliation"

	DefaultKafkaTopic   = "default"
	DefaultKafkaBroker  = "localhost:9092"
	DefaultKafkaGroupID = "reconciliation"

	CollPayments = "Payments"
	CollLedger   = "Ledger"
	CollRules    = "Rules"
)
