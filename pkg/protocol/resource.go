package protocol

import "github.com/qomos-w/gospore/resource"

// ManagerKey identifies the protocol manager in the actor resource registry.
var ManagerKey = resource.Key[*Manager]{Name: "protocol.manager"}
