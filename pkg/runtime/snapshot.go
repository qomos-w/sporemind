package runtime

// Actor state persistence is fully decentralised: each actor implements
// persist.Persistent and calls Load() in OnStart / Save() in OnStop.
// gospore's LIFO tree stop guarantees OnStop runs for every actor before
// the process exits, so no runtime coordinator is required.
//
// The only runtime-level persistence concern is the stable ActorID registry
// (see registry.go), which maps actor type + name -> ActorID across restarts.
