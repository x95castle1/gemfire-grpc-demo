package demo;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.ClientCallStreamObserver;
import io.grpc.stub.ClientResponseObserver;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.function.Consumer;

/**
 * One client-streaming or bidirectional call, driven from a plain thread.
 *
 * <p>Pass it to an async stub method (e.g. {@code GemFireGrpc.newStub(ch).putStream(call)}), then
 * {@link #send} messages, {@link #halfClose()} and {@link #await}. Two things the raw
 * {@code StreamObserver} API leaves to you:
 *
 * <ul>
 *   <li><b>Flow control.</b> {@code onNext} never blocks: it queues the message even when the
 *       server is slower than the client, so a big stream can pile up in client memory.
 *       {@link #send} waits until the transport is ready for more ({@code isReady()}).
 *   <li><b>Early failure.</b> If the server fails the call partway through (e.g. INVALID_ARGUMENT
 *       on a malformed chunk), {@link #send} throws instead of sending into a dead call.
 * </ul>
 *
 * Responses (for GetStream) are handed to {@code onResponse} on a gRPC thread.
 */
final class StreamCall<Q, R> implements ClientResponseObserver<Q, R> {

  private final Consumer<R> onResponse;
  private final CompletableFuture<Void> done = new CompletableFuture<>();
  private final Object ready = new Object();
  private ClientCallStreamObserver<Q> requests;
  private long sent;

  StreamCall(Consumer<R> onResponse) {
    this.onResponse = onResponse;
  }

  StreamCall() {
    this(r -> {});
  }

  @Override
  public void beforeStart(ClientCallStreamObserver<Q> requests) {
    this.requests = requests;
    requests.setOnReadyHandler(() -> {
      synchronized (ready) {
        ready.notifyAll();
      }
    });
  }

  /** Sends one message, first waiting for the transport to be ready for it. */
  void send(Q message) {
    synchronized (ready) {
      while (!requests.isReady() && !done.isDone()) {
        try {
          ready.wait(50);
        } catch (InterruptedException e) {
          Thread.currentThread().interrupt();
          requests.cancel("interrupted", e);
          throw Status.CANCELLED.withCause(e).asRuntimeException();
        }
      }
    }
    if (done.isCompletedExceptionally()) throw failure();
    requests.onNext(message);
    sent++;
  }

  /** Messages sent so far. */
  long sent() {
    return sent;
  }

  /** No more messages from the client; the server finishes the call. */
  void halfClose() {
    requests.onCompleted();
  }

  /** Waits for the server to close the call. Throws its error as a StatusRuntimeException. */
  void await(long timeout, TimeUnit unit) {
    try {
      done.get(timeout, unit);
    } catch (ExecutionException e) {
      throw failure();
    } catch (TimeoutException e) {
      requests.cancel("timed out", e);
      throw Status.DEADLINE_EXCEEDED.withDescription("no response in " + timeout + " " + unit).asRuntimeException();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      requests.cancel("interrupted", e);
      throw Status.CANCELLED.withCause(e).asRuntimeException();
    }
  }

  /** True once the server has closed the call (successfully or not). */
  boolean isDone() {
    return done.isDone();
  }

  private StatusRuntimeException failure() {
    Throwable t = done.exceptionNow();
    return t instanceof StatusRuntimeException s ? s : Status.fromThrowable(t).asRuntimeException();
  }

  @Override
  public void onNext(R response) {
    onResponse.accept(response);
  }

  @Override
  public void onError(Throwable t) {
    done.completeExceptionally(t);
    synchronized (ready) {
      ready.notifyAll();
    }
  }

  @Override
  public void onCompleted() {
    done.complete(null);
  }
}
