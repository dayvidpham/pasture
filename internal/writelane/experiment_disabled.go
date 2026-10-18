//go:build !pasture_experiment

package writelane

func (*Lane) observeExperimentQueue()              {}
func (l *Lane) flushBatch(batch []*item, _ string) { l.commitBatch(batch) }
