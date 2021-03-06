# State machine in Go 

Let's implement a simple state machine in Go that is:
1. easy to understand
2. easy to extend
3. easy to test

The full code is available in the [repo](https://github.com/kyeett/sqlc-order-processor).

#### Dependencies
* [kylenconroy/sqlc](https://github.com/kyleconroy/sqlc) - generate code from sql queries
* [matryer/moq](https://https://github.com/matryer/moq) - mock database layer for testing

## The State Machine 
We will implement a simple **order processor**. An order can be pretty much anything!
Our state machine will contain the following (main) states:

1. **created** - when order has been created, but not processed
2. **validated** - the order has been validated somehow
3. **broadcasted** - some data about the order has been broadcasted to other services
4. **complete** - all steps have been completed. This is the end state of our state machine

Exactly what happens in each individual step is not important here, as we will focus on building our state machine. 

### Code

#### Data layer
Our data model is very straightforward. Each order has an ID and a state. The state will be used for the state machine.
It was generated by [sqlc](https://github.com/kyleconroy/sqlc), but could of course be written by manually.
```go
type Order struct {
	ID    int64
	State string
}
```
If we include configuration `emit_interface: true` into our sqlc.yaml we will get an interface that we can use for database mocking, which makes testing a lot easier.
It looks like this:
```go
type Querier interface {
	CreateOrder(ctx context.Context, state string) (Order, error)
	GetOrder(ctx context.Context, id int64) (Order, error)
	UpdateOrderState(ctx context.Context, arg UpdateOrderStateParams) error
}
```

#### Application layer

```go
type orderProcessor struct {
	database data.Querier
}
```

Our `orderProcessor` struct contains the `Querier` generated by `sqlc`, based on the the data layer. The `orderProcessor` has the following main methods:

**CreateNewOrder** - creates a new order with the initial state "created".
```go
func (p *orderProcessor) CreateNewOrder(ctx context.Context) (*data.Order, error) {
	order, err := p.database.CreateOrder(ctx, stateCreated)
	if err != nil {
		return nil, err
	}
	return &order, nil
}
```

**StartProcessOrder** - takes an orderID, and iterates through each step of the state machine until the end state is reached, or an error has occurred.
```go
func (p *orderProcessor) StartProcessOrder(ctx context.Context, orderID int64) error {
	for {
		var isEndState bool
		isEndState, err := p.process(ctx, orderID)
		if err != nil {
			return err
		}

		if isEndState {
			return nil
		}
	}
}
```

**process** - performs an action based on the current state of the order and takes the order to the next step in the state machine.
```go
func (p *orderProcessor) process(ctx context.Context, orderID int64) (bool, error) {
	order, err := p.database.GetOrder(ctx, orderID)
	if err != nil {
		return false, err
	}
	
	switch order.State {
	case stateCreated:  
		return false, p.validateOrder(ctx, &order)
	case stateValidated:
		return false, p.updateOtherServices(ctx, &order)
	case stateBroadcastToOtherServices:
		return true, p.finalizeOrder(ctx, &order)
	default:
		return false, fmt.Errorf("unexpected state: %q", order.State)
	}
}
```
1. Get the order from the database
2. Based on the state, choose an action - 
3. Perform action

Example
1. Get order `123` 
2. It has state `created`, the next action is `validateOrder`. 
3. `validateOrder` will in turn do two things: 
   1. Validate order 
   2. If successful, move order to state `validated` (or maybe `failed`)

```go
func (p *orderProcessor) validateOrder(ctx context.Context, order *data.Order) error {
	
	// ... Code to validate order here 

	// Update state
	update := data.UpdateOrderStateParams{stateValidated, order.ID}
	if err := p.database.UpdateOrderState(ctx, update); err != nil {
		return err
	}
	return nil
}
```

That's it!

## Improvements

### 1. Extending the state machine
To add an additional step after order validation, we just add an additional action, after `stateValidated` and an add the corresponding state `addressFound`.
```diff
	...	
 	case stateCreated:
 		return false, p.validateOrder(ctx, &order)
 	case stateValidated:
+		return false, p.lookupAddress(ctx, &order)
+	case stateAddressFound:
 		return false, p.updateOtherServices(ctx, &order)
 	case stateBroadcastToOtherServices:
 		return true, p.finalizeOrder(ctx, &order)
	...	
```

### 2. Testing with a `moq` mock

To test the database layer in unit tests, we generate a mock using [moq](https://https://github.com/matryer/moq):
```sh
moq -out data/query_mock.go data Querier
```
And create setup a simple database mock that handles a single order
```go
type singleOrderDB struct {
	order data.Order
	*data.QuerierMock
}

func CreateTestDatabase(t *testing.T) singleOrderDB {
	db := singleOrderDB{}

	// Setup mock
	db.QuerierMock = &data.QuerierMock{
		CreateOrderFunc: func(ctx context.Context, state string) (data.Order, error) {
			db.order.State = state
			return db.order, nil
		},
		GetOrderFunc: func(ctx context.Context, id int64) (data.Order, error) {
			return db.order, nil
		},
		UpdateOrderStateFunc: func(ctx context.Context, arg data.UpdateOrderStateParams) error {
			switch db.order.State {
			case stateCreated:
				require.Equal(t, stateValidated, arg.State)
			case stateValidated:
				require.Equal(t, stateBroadcastToOtherServices, arg.State)
			case stateBroadcastToOtherServices:
				require.Equal(t, stateCompleted, arg.State)
			}
			db.order.State = arg.State
			return nil
		},
	}

	return db
}
``` 
Next we can write our test that 
1. Creates an order
2. Validates that the order ends up in `completed`
```go
func TestStateMachine(t *testing.T) {
	testDB := CreateTestDatabase(t)
	processor := NewProcessor(testDB)
	ctx := context.Background()

	// Arrange
	order, err := processor.CreateNewOrder(ctx)
	require.NoError(t, err)

	// Act
	err = processor.StartProcessOrder(ctx, order.ID)

	// Assert
	require.NoError(t, err)

	state, err := processor.GetOrderState(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, stateCompleted, state)
}
````
### 3. Intermittent states
One limitation of our state machine is that if an order step failed, we have no way of knowning if we 1) tried to processed the order but failed, or 2) never tried to process the order.
We can improve the robustness of our system by introducing temporary states, which represent the transitions between states. Something like this: 

1. **created**
2. ***validation_started***
3. **validation_complete**
4. ***broadcast_started***
5. **broadcast_complete**
4. **complete**

`updateOtherServices` will now look something like this
```go
func (p *orderProcessor) updateOtherServices(ctx context.Context, order *data.Order) error {
	if err := p.updateOrderState(ctx, order, stateBroadcastStarted); err != nil {
		return err
	}
	// Update other services

	// Update state
	return p.updateOrderState(ctx, order, stateBroadcastComplete)
}
```
If the actual call to update other services fails, we will know it, but the order being in `broadcast_started`. Depending on the operation, we can introduce automatic recovery for such events. Either by moving the order back to state `validated_complete` or maybe check if order has been broadcast before failure, and then move it to `broadcast_complete`.

### 4. "Locking" orders

A secondary benefit of these temporary states is that they can serve as a sort of lock. This is useful  if we want to run multiple processing processes at the same time. For example, a cron job that finds orders that has not been processed, and processes them.  While an order is in an transition state `validation_started`,`broadcast_started`, they should not be selected for processing. 

To avoid race conditions, we also need to check that the order is in the expected state, when we move to being processed.

The final form of ```updateOtherServices``` 
```go
func (p *orderProcessor) updateOtherServices(ctx context.Context, order *data.Order, expectedState string) error {
	if err := p.updateOrderState(ctx, order, stateBroadcastStarted, expectedState); err != nil {
		return err
	}
	// Update other services

	// Update state
	return p.updateOrderState(ctx, order, stateBroadcastComplete, stateBroadcastStarted)
}
``` 

## Wrap up

State machines are straight forward, and fun to implement and can increase the robustness of your application. They are useful for asynchronous processes, and when we need to recover gracefully from failures.

We covered how to implement a one way, one path state machine. Other topics that might be worth covering:
* Multiple paths
* Possibility to move back to visited states
* Failure states 
* Trigger state transitions from events from other systems
* Wait for manual approval (similar to above)
* ...