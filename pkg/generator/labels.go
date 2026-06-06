package generator

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

const (
	// LabelManagedBy identifies objects the operator manages.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelPackage is the prune key — every emitted object carries
	// `bigbang.dev/package=<package-name>`. The reconciler lists by this
	// label to compute the prune diff.
	LabelPackage = "bigbang.dev/package"
	// LabelNetpolSource matches the marker bb-common puts on every
	// NetworkPolicy it generates. We reproduce it for parity.
	LabelNetpolSource = "network-policies.bigbang.dev/source"
	// LabelNetpolSourceValue is the value used for the operator's NetworkPolicies.
	LabelNetpolSourceValue = "bigbang-operator"
	// FieldManager is the SSA field manager string.
	FieldManager = "bigbang-operator"
)

// stampMetadata sets the namespace, owner reference, and shared labels on
// obj. It does NOT clobber user-supplied labels — those merge in.
func stampMetadata(pkg *bbv1alpha1.Package, obj client.Object) {
	if obj.GetNamespace() == "" {
		obj.SetNamespace(pkg.Namespace)
	}

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if _, ok := labels[LabelManagedBy]; !ok {
		labels[LabelManagedBy] = FieldManager
	}
	labels[LabelPackage] = pkg.Name
	obj.SetLabels(labels)

	owner := metav1.OwnerReference{
		APIVersion:         bbv1alpha1.GroupVersion.String(),
		Kind:               "Package",
		Name:               pkg.Name,
		UID:                pkg.UID,
		Controller:         ptr(true),
		BlockOwnerDeletion: ptr(true),
	}
	// Replace existing controller ref for this Package; preserve unrelated refs.
	refs := obj.GetOwnerReferences()
	replaced := false
	for i := range refs {
		if refs[i].UID == owner.UID {
			refs[i] = owner
			replaced = true
			break
		}
	}
	if !replaced {
		refs = append(refs, owner)
	}
	obj.SetOwnerReferences(refs)
}

func ptr[T any](v T) *T { return &v }
