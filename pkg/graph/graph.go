package graph

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	messagingv1alpha1 "github.com/knative/eventing/pkg/apis/messaging/v1alpha1"
	servingv1beta1 "github.com/knative/serving/pkg/apis/serving/v1beta1"
	duckv1alpha1 "github.com/n3wscott/graph/pkg/apis/duck/v1alpha1"
	"github.com/tmc/dot"
)

type Graph struct {
	*dot.Graph
	nodes     map[string]*dot.Node
	subgraphs map[string]*dot.SubGraph
	dnsToKey  map[string]string // maps domain name to node key

	edgeCount   int
	rainbowEdge bool
}

func New(ns string) *Graph {
	g := dot.NewGraph("G")
	_ = g.Set("shape", "box")
	_ = g.Set("label", "Triggers in "+ns)
	_ = g.Set("rankdir", "LR")
	//_ = g.Set("compound", "true")

	graph := &Graph{
		Graph:       g,
		nodes:       make(map[string]*dot.Node),
		subgraphs:   make(map[string]*dot.SubGraph),
		dnsToKey:    make(map[string]string),
		rainbowEdge: true,
	}

	return graph
}

func (g *Graph) newEdge(src, dst *dot.Node) *dot.Edge {
	e := dot.NewEdge(src, dst)
	if g.rainbowEdge {
		color := colors[g.edgeCount%len(colors)]
		_ = e.Set("color", color)
		g.edgeCount++
	}
	return e
}

func (g *Graph) AddChannel(channel eventingv1alpha1.Channel) {
	ck := channelKey(channel.Name)
	uri := channel.Status.Address.GetURL()
	dns := strings.TrimSuffix((&uri).String(), "/")
	cn := dot.NewNode("Channel " + channel.Name)

	setNodeShapeForKind(cn, channel.Kind, channel.APIVersion)

	_ = cn.Set("shape", "oval") // TODO move to setNodeShapeForKind
	_ = cn.Set("label", "Ingress")

	g.nodes[ck] = cn
	g.dnsToKey[dns] = ck

	cg := dot.NewSubgraph(fmt.Sprintf("cluster_%d", len(g.subgraphs)))
	_ = cg.Set("label", fmt.Sprintf("Channel %s\n%s", channel.Name, dns))
	g.subgraphs[ck] = cg
	cg.AddNode(cn)
	g.AddSubgraph(cg)
}

func (g *Graph) AddSubscription(subscription eventingv1alpha1.Subscription) {
	sk := subscriptionKey(subscription.Name)
	sn := dot.NewNode("Subscription " + subscription.Name)

	ck := gvkKey(subscription.Spec.Channel.GroupVersionKind(), subscription.Spec.Channel.Name)

	if cg, ok := g.subgraphs[ck]; !ok {
		g.AddNode(sn)
	} else {
		cg.AddNode(sn)
	}
	g.nodes[sk] = sn

	if sub := g.getOrCreateSubscriber(subscription.Spec.Subscriber); sub != nil {
		e := dot.NewEdge(sn, sub)
		_ = e.Set("dir", "both")
		g.AddEdge(e)
	}

	if rep := g.getOrCreateReply(subscription.Spec.Reply); rep != nil {
		e := g.newEdge(sn, rep)
		_ = e.Set("dir", "forward")
		g.AddEdge(e)
	}
}

func (g *Graph) AddBroker(broker eventingv1alpha1.Broker) {
	key := brokerKey(broker.Name)
	uri := broker.Status.Address.GetURL()
	dns := strings.TrimSuffix((&uri).String(), "/")
	bn := dot.NewNode("Broker " + dns)
	_ = bn.Set("shape", "oval")
	_ = bn.Set("label", "Ingress")

	g.nodes[key] = bn
	g.dnsToKey[dns] = key

	bg := dot.NewSubgraph(fmt.Sprintf("cluster_%d", len(g.subgraphs)))
	_ = bg.Set("label", fmt.Sprintf("Broker %s\n%s", broker.Name, dns))
	g.subgraphs[key] = bg
	bg.AddNode(bn)
	g.AddSubgraph(bg)
}

func (g *Graph) AddSource(source duckv1alpha1.SourceType) {
	key := gvkKey(source.GroupVersionKind(), source.Name)
	sn := dot.NewNode(fmt.Sprintf("Source %s\nKind: %s\n%s", source.Name, source.Kind, source.APIVersion))
	_ = sn.Set("shape", "box")
	g.AddNode(sn)
	g.nodes[key] = sn

	sink := sinkDNS(source)

	if sink != "" {
		var bn *dot.Node
		var bk string
		var ok bool
		if bk, ok = g.dnsToKey[sink]; !ok {
			// TODO: unknown sink.
			bn = dot.NewNode("UnknownSink " + sink)
			g.AddNode(bn)
		} else {
			if bn, ok = g.nodes[bk]; !ok {
				// TODO: unknown broker.
				bn = dot.NewNode("UnknownSink " + sink)
				g.AddNode(bn)
			}
		}

		e := dot.NewEdge(sn, bn)
		if sg, ok := g.subgraphs[bk]; ok {
			// This is not working.
			_ = e.Set("lhead", sg.Name())
		}
		g.AddEdge(e)
	}
}

func (g *Graph) AddTrigger(trigger eventingv1alpha1.Trigger) {
	broker := trigger.Spec.Broker
	bk := brokerKey(broker)
	bn, ok := g.nodes[bk]
	if !ok {
		bn = dot.NewNode("UnknownBroker " + broker)
		g.AddNode(bn)
		g.nodes[bk] = bn
	}

	tn := dot.NewNode("Trigger " + trigger.Name)
	_ = tn.Set("shape", "box")

	if sg, ok := g.subgraphs[bk]; ok {
		sg.AddNode(tn)
	} else {
		g.AddNode(tn)
	}
	g.nodes[triggerKey(trigger.Name)] = tn

	if trigger.Spec.Filter != nil && trigger.Spec.Filter.SourceAndType != nil {
		label := fmt.Sprintf("Source:%s\nType:%s",
			trigger.Spec.Filter.SourceAndType.Source,
			trigger.Spec.Filter.SourceAndType.Type,
		)
		_ = tn.Set("label", fmt.Sprintf("%s\n%s", tn.Name(), label))
	}

	if sub := g.getOrCreateSubscriber(trigger.Spec.Subscriber); sub != nil {
		e := dot.NewEdge(tn, sub)
		_ = e.Set("dir", "both")
		g.AddEdge(e)
	}
}

func (g *Graph) AddKnService(service servingv1beta1.Service) {
	/*
	   spec:
	     runLatest:
	       configuration:
	         revisionTemplate:
	           metadata:
	             creationTimestamp: null
	           spec:
	             container:
	               env:
	               - name: TARGET
	                 value: http://default-broker.default.svc.cluster.local/
	*/

	config := service.Spec.ConfigurationSpec
	key := servingKey(service.Kind, service.Name)

	var svc *dot.Node
	var ok bool
	label := ""
	if svc, ok = g.nodes[key]; !ok {
		label = fmt.Sprintf("%s\nKind: %s\n%s",
			service.Name,
			service.Kind,
			service.APIVersion,
		)
		svc = dot.NewNode(label)
		setNodeShapeForKind(svc, service.Kind, service.APIVersion)

		_ = svc.Set("shape", "septagon")

		g.nodes[key] = svc
		g.AddNode(svc)
	}

	for _, env := range config.Template.Spec.Containers[0].Env {
		switch env.Name {
		case "SINK":
			fallthrough
		case "TARGET":
			// Assume full dns name.
			target := g.getOrCreateSink(env.Value)
			e := dot.NewEdge(svc, target)
			g.AddEdge(e)
		}
	}
}

func (g *Graph) AddSequence(seq messagingv1alpha1.Sequence) {

	key := sequenceKey(seq.Name)

	uri := seq.Status.Address.GetURL()
	dns := strings.TrimSuffix((&uri).String(), "/")

	sg := dot.NewSubgraph(fmt.Sprintf("cluster_%d", len(g.subgraphs)))
	_ = sg.Set("label", fmt.Sprintf("Sequence %s\n%s", seq.Name, dns))
	//	_ = sg.Set("rankdir", "BT")

	g.dnsToKey[dns] = key
	sn := dot.NewNode("Sequence " + dns)
	_ = sn.Set("label", "Start")
	//	_ = sn.Set("rank", "min")

	g.nodes[key] = sn
	sg.AddNode(sn)

	previousNode := sn

	for num, step := range seq.Spec.Steps {
		stepKey := sequenceStepKey(seq.Name, num)
		stepn := dot.NewNode(stepKey)
		_ = stepn.Set("label", fmt.Sprintf("Step %d", num))
		_ = stepn.Set("shape", "box")

		// Add to seq subgraph.
		sg.AddNode(stepn)

		g.nodes[stepKey] = stepn

		if sub := g.getOrCreateSubscriber(&step); sub != nil {
			e := dot.NewEdge(stepn, sub)
			_ = e.Set("dir", "both")
			g.AddEdge(e)
		}

		e := dot.NewEdge(previousNode, stepn)
		g.AddEdge(e)
		previousNode = stepn
	}

	if seq.Spec.Reply != nil {
		replyn := dot.NewNode("Reply " + dns)
		_ = replyn.Set("label", "Reply")
		//_ = replyn.Set("rank", "max")
		//g.nodes[] = rn
		sg.AddNode(replyn)

		// TODO where this points.
		e := dot.NewEdge(previousNode, replyn)
		g.AddEdge(e)

		rk := gvkKey(seq.Spec.Reply.GroupVersionKind(), seq.Spec.Reply.Name)
		if rn, ok := g.nodes[rk]; ok {
			e := dot.NewEdge(replyn, rn)
			g.AddEdge(e)
		}
	}

	g.subgraphs[key] = sg
	g.AddSubgraph(sg)

}

func setNodeShapeForKind(node *dot.Node, kind, apiVersion string) {
	if apiVersion == "serving.knative.dev/v1beta1" {
		switch kind {
		case "Service":
			_ = node.Set("shape", "septagon")
		}
	}
}

func (g *Graph) getOrCreateSink(uri string) *dot.Node {
	uri = strings.TrimSuffix(uri, "/")

	var node *dot.Node
	var key string
	var ok bool
	if key, ok = g.dnsToKey[uri]; !ok {
		node = dot.NewNode("UnknownSink " + uri)
		g.AddNode(node)
	}
	return g.nodes[key]
}

func (g *Graph) getOrCreateSubscriber(subscriber *eventingv1alpha1.SubscriberSpec) *dot.Node {
	key := "?"
	label := "?"

	if subscriber != nil {
		if subscriber.URI != nil {
			label = *subscriber.URI
			key = uriKey(*subscriber.URI)
		} else if subscriber.Ref != nil {
			label = fmt.Sprintf("%s\nKind: %s\n%s",
				subscriber.Ref.Name,
				subscriber.Ref.Kind,
				subscriber.Ref.APIVersion,
			)
			key = refKey(
				subscriber.Ref.APIVersion,
				subscriber.Ref.Kind,
				subscriber.Ref.Name,
			)
		}
	}
	var sub *dot.Node
	var ok bool
	if sub, ok = g.nodes[key]; !ok {
		sub = dot.NewNode(label)
		if subscriber != nil && subscriber.Ref != nil {
			setNodeShapeForKind(sub, subscriber.Ref.Kind, subscriber.Ref.APIVersion)
		}

		g.nodes[key] = sub
		g.AddNode(sub)
	}
	return sub
}

func (g *Graph) getOrCreateReply(rep *eventingv1alpha1.ReplyStrategy) *dot.Node {
	if rep != nil && rep.Channel != nil {
		ck := channelKey(rep.Channel.Name)
		if cn, ok := g.nodes[ck]; !ok {
			cn = dot.NewNode("Unknown Channel " + rep.Channel.Name)
		} else {
			return cn
		}
	}
	return nil
}

func sinkDNS(source duckv1alpha1.SourceType) string {
	if source.Status.SinkURI != nil {
		return strings.TrimSuffix(*(source.Status.SinkURI), "/")
	}
	return ""
}

func channelKey(name string) string {
	return eventingKey("channel", name)
}

func subscriptionKey(name string) string {
	return eventingKey("subscription", name)
}

func brokerKey(name string) string {
	return eventingKey("broker", name)
}

func triggerKey(name string) string {
	return eventingKey("trigger", name)
}

func sequenceKey(name string) string {
	return messagingKey("sequence", name)
}

func sequenceStepKey(name string, step int) string {
	return messagingKey("sequencestep", fmt.Sprintf("%s-%d", name, step))
}

func gvkKey(gvk schema.GroupVersionKind, name string) string {
	return strings.ToLower(fmt.Sprintf("%s/%s/%s/%s", gvk.Group, gvk.Version, gvk.Kind, name))
}

func key(group, version, kind, name string) string {
	return strings.ToLower(fmt.Sprintf("%s/%s/%s/%s", group, version, kind, name))
}

func uriKey(uri string) string {
	return strings.ToLower(fmt.Sprintf("uri/%s", uri))
}

func refKey(apiVersion, kind, name string) string {
	return strings.ToLower(fmt.Sprintf("%s/%s/%s", apiVersion, kind, name))
}

func eventingKey(kind, name string) string {
	return key("eventing.knative.dev", "v1alpha1", kind, name)
}

func messagingKey(kind, name string) string {
	return key("messaging.knative.dev", "v1alpha1", kind, name)
}

func servingKey(kind, name string) string {
	return key("serving.knative.dev", "v1beta1", kind, name)
}
