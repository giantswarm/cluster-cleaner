[![CircleCI](https://circleci.com/gh/giantswarm/cluster-cleaner.svg?style=shield)](https://circleci.com/gh/giantswarm/cluster-cleaner)

# cluster-cleaner

This operator is intended to automate deletion of giant swarm workload test clusters. By default your cluster will be deleted after 10 hours.

## how to prevent cluster from being deleted

To prevent your cluster from being deleted you can set one of two annotations.

1. Your cluster wont' be deleted until you remove the annotation:

```
annotations:
  alpha.giantswarm.io/ignore-cluster-deletion: "true"
```

2. Your cluster will be deleted after the date you've set expired.

```
annotations:
	keep-until: "2022-02-01"
```

## observability

The operator exposes a couple of prometheus metrics.

- `deletion_ignored_total`: the number of all ignored cluster deletion.
- `deletion_pending_total`: the number of all pending cluster deletion.
- `deletion_errors_total`: the number of all failed cluster deletion.
- `deletion_succeeded_total`: the number of all clusters that were deleted successfully.
